package ufop

import (
	"io"
)

const (
	RESULT_TYPE_JSON = iota
	RESULT_TYPE_OCTET_BYTES
	RESULT_TYPE_OCTET_FILE
	RESULT_TYPE_OCTET_URL
)

const (
	CONTENT_TYPE_JSON  = "application/json;charset=utf-8"
	CONTENT_TYPE_OCTET = "application/octet-stream"
)

type UfopRequest struct {
	Cmd   string `json:"cmd"`
	Url   string `json:"url"`
	ReqId string `json:"-"`
}

type UfopError struct {
	Request UfopRequest
	Error   string
}

type UfopJobHandler interface {
	Name() string
	InitConfig(jobConf string) error
	Do(ufopReq UfopRequest, ufopBody io.ReadCloser) (interface{}, int, string, error)
}
