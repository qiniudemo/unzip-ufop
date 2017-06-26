package unzip

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
	"ufop"
	"ufop/utils"
	"unicode/utf8"

	"github.com/qiniu/api.v6/auth/digest"
	"github.com/qiniu/api.v6/conf"
	fio "github.com/qiniu/api.v6/io"
	rio "github.com/qiniu/api.v6/resumable/io"
	"github.com/qiniu/api.v6/rs"
	"github.com/qiniu/log"
	"github.com/qiniu/rpc"
)

const (
	UNZIP_MAX_ZIP_FILE_LENGTH int64 = 1 * 1024 * 1024 * 1024
	UNZIP_MAX_FILE_LENGTH     int64 = 100 * 1024 * 1024 //100MB
	UNZIP_MAX_FILE_COUNT      int   = 10                //10
)

const (
	UNZIP_CACHE_ZIP_FILE_THRESHOLD  = 20 * 1024 * 1024 //20MB
	UNZIP_CACHE_FILE_ITEM_THRESHOLD = 20 * 1024 * 1024 //20MB
)

const (
	RESUMABLE_PUT_THRESHOLD = 20 * 1024 * 1024
)

type UnzipResult struct {
	Files []UnzipFile `json:"files"`
}

type UnzipFile struct {
	Key   string `json:"key"`
	Hash  string `json:"hash,omitempty"`
	Error string `json:"error,omitempty"`
}

type Unzipper struct {
	mac              *digest.Mac
	maxZipFileLength int64
	maxFileLength    int64
	maxFileCount     int
}

type UnzipperConfig struct {
	//ak & sk
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`

	UnzipMaxZipFileLength int64 `json:"unzip_max_zip_file_length,omitempty"`
	UnzipMaxFileLength    int64 `json:"unzip_max_file_length,omitempty"`
	UnzipMaxFileCount     int   `json:"unzip_max_file_count,omitempty"`
}

func (this *Unzipper) Name() string {
	return "unzip"
}

func (this *Unzipper) InitConfig(jobConf string) (err error) {
	confFp, openErr := os.Open(jobConf)
	if openErr != nil {
		err = errors.New(fmt.Sprintf("Open unzip config failed, %s", openErr.Error()))
		return
	}

	config := UnzipperConfig{}
	decoder := json.NewDecoder(confFp)
	decodeErr := decoder.Decode(&config)
	if decodeErr != nil {
		err = errors.New(fmt.Sprintf("Parse unzip config failed, %s", decodeErr.Error()))
		return
	}

	if config.UnzipMaxFileCount <= 0 {
		this.maxFileCount = UNZIP_MAX_FILE_COUNT
	} else {
		this.maxFileCount = config.UnzipMaxFileCount
	}

	if config.UnzipMaxFileLength <= 0 {
		this.maxFileLength = UNZIP_MAX_FILE_LENGTH
	} else {
		this.maxFileLength = config.UnzipMaxFileLength
	}

	if config.UnzipMaxZipFileLength <= 0 {
		this.maxZipFileLength = UNZIP_MAX_ZIP_FILE_LENGTH
	} else {
		this.maxZipFileLength = config.UnzipMaxZipFileLength
	}

	this.mac = &digest.Mac{config.AccessKey, []byte(config.SecretKey)}

	return
}

/*

unzip/bucket/<encoded bucket>/prefix/<encoded prefix>/overwrite/<[0|1]>

*/
func (this *Unzipper) parse(cmd string) (bucket string, prefix string, overwrite bool, err error) {
	pattern := "^unzip/bucket/[0-9a-zA-Z-_=]+(/prefix/[0-9a-zA-Z-_=]+){0,1}(/overwrite/(0|1)){0,1}$"
	matched, _ := regexp.MatchString(pattern, cmd)
	if !matched {
		err = errors.New("invalid unzip command format")
		return
	}

	var decodeErr error
	bucket, decodeErr = utils.GetParamDecoded(cmd, "bucket/[0-9a-zA-Z-_=]+", "bucket")
	if decodeErr != nil {
		err = errors.New("invalid unzip parameter 'bucket'")
		return
	}
	prefix, decodeErr = utils.GetParamDecoded(cmd, "prefix/[0-9a-zA-Z-_=]+", "prefix")
	if decodeErr != nil {
		err = errors.New("invalid unzip parameter 'prefix'")
		return
	}
	overwriteStr := utils.GetParam(cmd, "overwrite/(0|1)", "overwrite")
	if overwriteStr != "" {
		overwriteVal, paramErr := strconv.ParseInt(overwriteStr, 10, 64)
		if paramErr != nil {
			err = errors.New("invalid unzip parameter 'overwrite'")
			return
		}
		if overwriteVal == 1 {
			overwrite = true
		}
	}
	return
}

func (this *Unzipper) Do(req ufop.UfopRequest, ufopBody io.ReadCloser) (result interface{}, resultType int,
	contentType string, err error) {
	//parse command
	bucket, prefix, overwrite, pErr := this.parse(req.Cmd)
	if pErr != nil {
		err = pErr
		return
	}

	log.Infof("[%s] downloading file", req.ReqId)
	//get resource
	resUrl := req.Url
	resResp, respErr := http.Get(resUrl)
	if respErr != nil || resResp.StatusCode != 200 {
		if respErr != nil {
			err = fmt.Errorf("retrieve resource data failed, %s", respErr.Error())
		} else {
			err = fmt.Errorf("retrieve resource data failed, %s", resResp.Status)
			if resResp.Body != nil {
				resResp.Body.Close()
			}
		}
		return
	}
	defer resResp.Body.Close()
	reqSrcSize := resResp.ContentLength
	reqSrcMime := resResp.Header.Get("Content-Type")

	log.Infof("[%s] content length: %d, content type: %s", req.ReqId, reqSrcSize, reqSrcMime)
	//check mimetype
	//if !(reqSrcMime == "application/zip" || reqSrcMime == "application/x-zip-compressed") {
	//	err = errors.New("unsupported mimetype to unzip")
	//	return
	//}
	//check zip file length
	if reqSrcSize > this.maxZipFileLength {
		err = errors.New("src zip file length exceeds the limit")
		return
	}

	//zip
	var zipReader *zip.Reader
	var zipErr error
	//check the size of the src size file, when exceeds the threshold, use disk cache
	if reqSrcSize > UNZIP_CACHE_ZIP_FILE_THRESHOLD {
		log.Infof("[%s] trying to read zip into disk", req.ReqId)

		zipFileCacheFname := utils.Md5Hex(fmt.Sprintf("%s:%d", req.Url, time.Now().Unix()))
		zipFileCacheFpath := filepath.Join(os.TempDir(), zipFileCacheFname)
		zipFileCacheFh, openErr := os.Create(zipFileCacheFpath)
		defer os.Remove(zipFileCacheFpath)

		if openErr != nil {
			err = fmt.Errorf("open local zip cache file failed, %s", openErr.Error())
			return
		}
		_, cpErr := io.Copy(zipFileCacheFh, resResp.Body)
		if cpErr != nil {
			err = fmt.Errorf("write local zip cache file failed, %s", cpErr.Error())
			return
		}
		zipFileCacheFh.Close()

		zipFileCacheFh, openErr = os.Open(zipFileCacheFpath)
		if openErr != nil {
			err = fmt.Errorf("reopen local zip cache file failed, %s", openErr.Error())
			return
		}
		zipFileCacheStat, statErr := zipFileCacheFh.Stat()
		if statErr != nil {
			err = fmt.Errorf("reopen local zip cache file size error, %s", statErr.Error())
			return
		}
		zipReader, zipErr = zip.NewReader(zipFileCacheFh, zipFileCacheStat.Size())
		if zipErr != nil {
			err = errors.New(fmt.Sprintf("invalid zip file, %s", zipErr.Error()))
			return
		}
	} else {
		log.Infof("[%s] trying to read zip into memory", req.ReqId)
		respData, readErr := ioutil.ReadAll(resResp.Body)
		if readErr != nil {
			err = errors.New(fmt.Sprintf("read resource data failed, %s", readErr.Error()))
			return
		}

		//read zip
		respReader := bytes.NewReader(respData)
		zipReader, zipErr = zip.NewReader(respReader, int64(respReader.Len()))
		if zipErr != nil {
			err = errors.New(fmt.Sprintf("invalid zip file, %s", zipErr.Error()))
			return
		}
	}

	log.Infof("[%s] check and start to unzip", req.ReqId)
	//iter zip files
	zipFiles := zipReader.File
	//check file count
	zipFileCount := len(zipFiles)
	if zipFileCount > this.maxFileCount {
		err = errors.New("zip files count exceeds the limit")
		return
	}
	//check file size
	for _, zipFile := range zipFiles {
		fileSize := zipFile.UncompressedSize64
		//check file size
		if int64(fileSize) > this.maxFileLength {
			err = errors.New("zip file length exceeds the limit")
			return
		}
	}

	log.Infof("[%s] start to upload files", req.ReqId)
	//set up host
	conf.UP_HOST = "http://up.qiniu.com"
	rputSettings := rio.Settings{
		ChunkSize: 4 * 1024 * 1024,
		Workers:   8,
	}
	rio.SetSettings(&rputSettings)
	policy := rs.PutPolicy{
		Scope: bucket,
	}
	policy.Expires = 24 * 3600 //24 hours

	var unzipResult UnzipResult
	unzipResult.Files = make([]UnzipFile, 0, 100)
	var tErr error
	//iterate the zip file

	for _, zipFile := range zipFiles {
		fileInfo := zipFile.FileHeader.FileInfo()
		fileName := zipFile.FileHeader.Name
		fileSize := zipFile.UncompressedSize64

		if !utf8.Valid([]byte(fileName)) {
			fileName, tErr = utils.Gbk2Utf8(fileName)
			if tErr != nil {
				err = errors.New(fmt.Sprintf("unsupported file name encoding, %s", tErr.Error()))
				return
			}
		}

		if fileInfo.IsDir() {
			continue
		}

		var unzipFile UnzipFile

		//save file to bucket
		fileKey := prefix + fileName
		unzipFile.Key = fileKey

		if overwrite {
			policy.Scope = bucket + ":" + fileKey
		}

		uptoken := policy.Token(this.mac)

		zipFileReader, zipErr := zipFile.Open()
		if zipErr != nil {
			err = errors.New(fmt.Sprintf("open zip file content failed, %s", zipErr.Error()))
			return
		}

		if fileSize > UNZIP_CACHE_FILE_ITEM_THRESHOLD {
			zipFileItemCacheFname := utils.Md5Hex(fmt.Sprintf("%s:%s:%d", req.Url, fileName, time.Now().Unix()))
			zipFileItemCacheFpath := filepath.Join(os.TempDir(), zipFileItemCacheFname)
			zipFileItemCacheFh, openErr := os.Create(zipFileItemCacheFpath)
			defer os.Remove(zipFileItemCacheFpath)

			if openErr != nil {
				err = fmt.Errorf("open local cache file item failed, %s", openErr.Error())
				return
			}

			_, cpErr := io.Copy(zipFileItemCacheFh, zipFileReader)
			if cpErr != nil {
				err = fmt.Errorf("write local cache file item failed, %s", cpErr.Error())
				zipFileItemCacheFh.Close()
				return
			}
			zipFileItemCacheFh.Close()
			zipFileReader.Close()

			if fileSize <= RESUMABLE_PUT_THRESHOLD {
				log.Infof("[%s] start to fput file %s", req.ReqId, fileName)
				var fputRet fio.PutRet
				fErr := fio.PutFile(nil, &fputRet, uptoken, fileKey, zipFileItemCacheFpath, nil)
				if fErr != nil {
					if v, ok := fErr.(*rpc.ErrorInfo); ok {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", v.Err)
					} else {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", fErr.Error())
					}
				} else {
					unzipFile.Hash = fputRet.Hash
				}
				log.Infof("[%s] end fput file %s", req.ReqId, fileName)
			} else {
				log.Infof("[%s] start to rput file %s", req.ReqId, fileName)
				var rputRet rio.PutRet
				rErr := rio.PutFile(nil, &rputRet, uptoken, fileKey, zipFileItemCacheFpath, nil)
				if rErr != nil {
					if v, ok := rErr.(*rpc.ErrorInfo); ok {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", v.Err)
					} else {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", rErr.Error())
					}
				} else {
					unzipFile.Hash = rputRet.Hash
				}
				log.Infof("[%s] end rput file %s", req.ReqId, fileName)
			}

		} else {
			unzipData, unzipErr := ioutil.ReadAll(zipFileReader)
			if unzipErr != nil {
				err = errors.New(fmt.Sprintf("unzip the file content failed, %s", unzipErr.Error()))
				zipFileReader.Close()
				return
			}
			zipFileReader.Close()
			unzipReader := bytes.NewReader(unzipData)

			if fileSize <= RESUMABLE_PUT_THRESHOLD {
				log.Infof("[%s] start to fput bytes %s", req.ReqId, fileName)
				var fputRet fio.PutRet
				fErr := fio.Put(nil, &fputRet, uptoken, fileKey, unzipReader, nil)
				if fErr != nil {
					if v, ok := fErr.(*rpc.ErrorInfo); ok {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", v.Err)
					} else {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", fErr.Error())
					}
				} else {
					unzipFile.Hash = fputRet.Hash
				}
				log.Infof("[%s] end fput bytes %s", req.ReqId, fileName)
			} else {
				log.Infof("[%s] start to rput bytes %s", req.ReqId, fileName)
				var rputRet rio.PutRet
				rErr := rio.Put(nil, &rputRet, uptoken, fileKey, unzipReader, int64(fileSize), nil)
				if rErr != nil {
					if v, ok := rErr.(*rpc.ErrorInfo); ok {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", v.Err)
					} else {
						unzipFile.Error = fmt.Sprintf("save unzip file to bucket error, %s", rErr.Error())
					}
				} else {
					unzipFile.Hash = rputRet.Hash
				}
				log.Infof("[%s] end rput bytes %s", req.ReqId, fileName)
			}
		}

		unzipResult.Files = append(unzipResult.Files, unzipFile)
	}

	log.Infof("[%s] upload files done", req.ReqId)
	//write result
	result = unzipResult
	resultType = ufop.RESULT_TYPE_JSON
	contentType = ufop.CONTENT_TYPE_JSON

	return
}
