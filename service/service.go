package service

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	pb "github.com/evilsocket/sum/proto"
	"github.com/evilsocket/sum/storage"
	"github.com/evilsocket/sum/wrapper"

	"golang.org/x/net/context"
)

const (
	// responses bigger than 2K will be gzipped
	gzipResponseSize  = 2048
	dataFolderName    = "data"
	oraclesFolderName = "oracles"
)

type Service struct {
	started  time.Time
	pid      uint64
	uid      uint64
	argv     []string
	records  *storage.Records
	wrecords wrapper.Records
	oracles  *storage.Oracles
}

func New(dataPath string) (*Service, error) {
	records, err := storage.LoadRecords(filepath.Join(dataPath, dataFolderName))
	if err != nil {
		return nil, err
	}

	oracles, err := storage.LoadOracles(filepath.Join(dataPath, oraclesFolderName))
	if err != nil {
		return nil, err
	}

	return &Service{
		started:  time.Now(),
		pid:      uint64(os.Getpid()),
		uid:      uint64(os.Getuid()),
		argv:     os.Args,
		records:  records,
		wrecords: wrapper.ForRecords(records),
		oracles:  oracles,
	}, nil
}

func (s *Service) Info(ctx context.Context, dummy *pb.Empty) (*pb.ServerInfo, error) {
	return &pb.ServerInfo{
		Version: Version,
		Uptime:  uint64(time.Since(s.started).Seconds()),
		Pid:     s.pid,
		Uid:     s.uid,
		Argv:    s.argv,
		Records: s.records.Size(),
		Oracles: s.oracles.Size(),
	}, nil
}

func errCallResponse(format string, args ...interface{}) *pb.CallResponse {
	return &pb.CallResponse{Success: false, Msg: fmt.Sprintf(format, args...)}
}

func (s *Service) Run(ctx context.Context, call *pb.Call) (*pb.CallResponse, error) {
	compiled := s.oracles.Find(call.OracleId)
	if compiled == nil {
		return errCallResponse("Oracle %d not found.", call.OracleId), nil
	}

	vm := compiled.VM()
	callCtx := wrapper.NewContext()

	vm.Set("records", s.wrecords)
	vm.Set("ctx", callCtx)

	var j []byte

	if ret, err := compiled.Run(call.Args); err != nil {
		return errCallResponse("Error while running oracle %s: %s", call.OracleId, err), nil
	} else if callCtx.IsError() {
		return errCallResponse("Error while running oracle %s: %s", call.OracleId, callCtx.Message()), nil
	} else if obj, err := ret.Export(); err != nil {
		return errCallResponse("Error while serializing return value of oracle %s: %s", call.OracleId, err), nil
	} else if j, err = json.Marshal(obj); err != nil {
		return errCallResponse("Error while marshaling return value of oracle %s: %s", call.OracleId, err), nil
	}

	data := &pb.Data{
		Compressed: false,
		Payload:    j,
	}

	jsonSize := len(j)
	if jsonSize > gzipResponseSize {
		var buf bytes.Buffer
		if w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression); err == nil {
			w.Write(j)
			w.Close()

			data.Compressed = true
			data.Payload = buf.Bytes()
		} else {
			log.Printf("compression failed: %s", err)
		}
	}

	return &pb.CallResponse{
		Success: true,
		Data:    data,
	}, nil
}
