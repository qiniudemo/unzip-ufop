package ufop

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qiniu/log"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"ufop/utils"
)

type UfopServer struct {
	cfg         *UfopConfig
	jobHandlers map[string]UfopJobHandler
}

func NewServer(cfg *UfopConfig) *UfopServer {
	serv := UfopServer{}
	serv.cfg = cfg
	serv.jobHandlers = make(map[string]UfopJobHandler, 0)
	return &serv
}

func (this *UfopServer) RegisterJobHandler(jobConf string, jobHandler interface{}) (err error) {
	if h, ok := jobHandler.(UfopJobHandler); ok {
		initErr := h.InitConfig(jobConf)
		if initErr != nil {
			err = errors.New(fmt.Sprintf("init job handler for cmd '%s' error, %s", h.Name(), initErr.Error()))
			return
		}

		this.jobHandlers[this.cfg.UfopPrefix+h.Name()] = h
	} else {
		err = errors.New(fmt.Sprintf("job handler of [%s] must implement interface UfopJobHandler", jobConf))
	}
	return
}

func (this *UfopServer) Listen() {
	//define handler
	http.HandleFunc("/handler", this.serveUfop)
	http.HandleFunc("/health", this.serveHealth)

	//bind and listen
	endPoint := fmt.Sprintf("%s:%d", this.cfg.ListenHost, this.cfg.ListenPort)
	ufopServer := &http.Server{
		Addr:           endPoint,
		ReadTimeout:    time.Duration(this.cfg.ReadTimeout) * time.Second,
		WriteTimeout:   time.Duration(this.cfg.WriteTimeout) * time.Second,
		MaxHeaderBytes: this.cfg.MaxHeaderBytes,
	}

	listenErr := ufopServer.ListenAndServe()
	if listenErr != nil {
		log.Println(listenErr)
	}
}

func (this *UfopServer) serveHealth(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (this *UfopServer) serveUfop(w http.ResponseWriter, req *http.Request) {
	//check method
	if req.Method != "POST" {
		writeJsonError(w, 405, "method not allowed")
		return
	}

	defer req.Body.Close()

	var err error

	//url parameter
	var ufopReq UfopRequest
	var ufopResult interface{}
	var ufopResultType int
	var ufopResultContentType string

	//parse form and set url
	req.ParseForm()
	ufopReq.Cmd = req.Form.Get("cmd")
	ufopReq.Url = req.Form.Get("url")

	reqId := utils.NewRequestId()
	ufopReq.ReqId = reqId
	ufopResult, ufopResultType, ufopResultContentType, err =
		handleJob(ufopReq, req.Body, this.cfg.UfopPrefix, this.jobHandlers)
	if err != nil {
		ufopErr := UfopError{
			Request: ufopReq,
			Error:   err.Error(),
		}
		logBytes, _ := json.Marshal(&ufopErr)
		log.Error(reqId, string(logBytes))
		writeJsonError(w, 400, err.Error())
	} else {
		switch ufopResultType {
		case RESULT_TYPE_JSON:
			writeJsonResult(w, 200, ufopResult)
		case RESULT_TYPE_OCTET_BYTES:
			writeOctetResultFromBytes(w, ufopResult, ufopResultContentType)
		case RESULT_TYPE_OCTET_FILE:
			writeOctetResultFromFile(w, ufopResult, ufopResultContentType)
		case RESULT_TYPE_OCTET_URL:
			writeOctetResultFromUrl(w, ufopResult)
		}
	}
}

func handleJob(ufopReq UfopRequest, ufopBody io.ReadCloser, ufopPrefix string,
	jobHandlers map[string]UfopJobHandler) (interface{}, int, string, error) {
	defer ufopBody.Close()
	var ufopResult interface{}
	var resultType int
	var contentType string
	var err error
	cmd := ufopReq.Cmd

	items := strings.SplitN(cmd, "/", 2)
	fop := items[0]
	if jobHandler, ok := jobHandlers[fop]; ok {
		ufopReq.Cmd = strings.TrimPrefix(ufopReq.Cmd, ufopPrefix)
		ufopResult, resultType, contentType, err = jobHandler.Do(ufopReq, ufopBody)
	} else {
		err = errors.New("no fop available for the request")
	}
	return ufopResult, resultType, contentType, err
}

func writeJsonError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	respErr := struct {
		Error string `json:"error"`
	}{
		Error: message,
	}
	respErrBytes, _ := json.Marshal(&respErr)
	_, err := w.Write(respErrBytes)
	if err != nil {
		log.Error("write error error", err)
	}
}

func writeJsonResult(w http.ResponseWriter, statusCode int, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	data, err := json.Marshal(result)
	if err != nil {
		log.Error("encode ufop result error,", err)
		writeJsonError(w, 500, "encode ufop result error")
	} else {
		_, err := io.WriteString(w, string(data))
		if err != nil {
			log.Error("write json response error", err)
		}
	}
}

func writeOctetResultFromBytes(w http.ResponseWriter, result interface{}, mimeType string) {
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	if respData := result.([]byte); respData != nil {
		_, err := w.Write(respData)
		if err != nil {
			log.Error("write octet from bytes error", err)
		}
	}
}

func writeOctetResultFromFile(w http.ResponseWriter, result interface{}, mimeType string) {
	//delete the tmp file
	var filePath string
	if v, ok := result.(string); ok {
		filePath = v
	}
	defer os.Remove(filePath)
	//set response
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	//output result
	resultFp, openErr := os.Open(filePath)
	if openErr != nil {
		log.Error("open result local file error", openErr)
		return
	}
	defer resultFp.Close()
	_, cpErr := io.Copy(w, resultFp)
	if cpErr != nil {
		log.Error("write octet from local file error", cpErr)
		return
	}
}

var copyHeaders = []string{
	"Content-Type",
	"Content-Length",
}

const TimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

func writeOctetResultFromUrl(w http.ResponseWriter, result interface{}) {
	var resUrl string
	if v, ok := result.(string); ok {
		resUrl = v
	}

	resp, respErr := http.Get(resUrl)
	if respErr != nil {
		log.Error("get remote resource error", respErr)
		return
	}
	defer resp.Body.Close()

	for _, header := range copyHeaders {
		if resp.Header.Get(header) != "" {
			w.Header().Set(header, resp.Header.Get(header))
		}
	}

	//write last modified
	w.Header().Set("Last-Modified", time.Now().UTC().Format(TimeFormat))

	_, cpErr := io.Copy(w, resp.Body)
	if cpErr != nil {
		log.Error("write octet from remote resource error", cpErr)
		return
	}
}
