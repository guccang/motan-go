package motan

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/weibocom/motan-go/core"
	mhttp "github.com/weibocom/motan-go/http"
	mpro "github.com/weibocom/motan-go/protocol"
)

const (
	goNum      = 5
	requestNum = 10000
)

var proxyClient *http.Client
var meshClient *MeshClient

func TestMain(m *testing.M) {
	cfgFile := filepath.Join("testdata", "agent.yaml")
	go func() {
		var addr = ":9090"
		handler := &http.ServeMux{}
		handler.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
			request.ParseForm()
			writer.Write([]byte(request.URL.String()))
		})
		http.ListenAndServe(addr, handler)
	}()
	go func() {
		agent := NewAgent(nil)
		agent.ConfigFile = cfgFile
		agent.StartMotanAgent()
	}()
	core.RegistLocalProvider("LocalTestService", &LocalTestServiceProvider{})
	proxyURL, _ := url.Parse("http://localhost:9983")
	proxyClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	time.Sleep(1 * time.Second)
	meshClient = NewMeshClient()
	meshClient.SetRequestTimeout(time.Second)
	meshClient.Initialize()
	for i := 0; i < 100; i++ {
		resp, err := proxyClient.Get("http://test.domain/tst/test")
		if err != nil {
			continue
		}
		if resp.StatusCode != 200 {
			continue
		}
		time.Sleep(1 * time.Second)
		break
	}
	os.Exit(m.Run())
}

func TestHTTPProxyBodySize(t *testing.T) {
	body := bytes.NewReader(make([]byte, 1000))
	resp, _ := proxyClient.Post("http://test.domain/tst/test", "application/octet-stream", body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = bytes.NewReader(make([]byte, 1001))
	resp, _ = proxyClient.Post("http://test.domain/tst/test", "application/octet-stream", body)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHTTPProxy(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(goNum)
	requests := make(chan int, requestNum)
	for i := 0; i < goNum; i++ {
		go func() {
			defer wg.Done()
			for req := range requests {
				suffix := "test" + strconv.Itoa(req)
				resp, err := proxyClient.Get("http://test.domain/tst/test?index=" + suffix)
				if err != nil {
					continue
				}
				bytes, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					resp.Body.Close()
					continue
				}
				resp.Body.Close()
				if !strings.HasSuffix(string(bytes), suffix) {
					t.Errorf("wrong response")
				}
			}
		}()
	}
	for i := 0; i < requestNum; i++ {
		requests <- i
	}
	close(requests)
	wg.Wait()
}

func TestRpcToHTTPProxy(t *testing.T) {
	service := "test.domain"
	request := meshClient.BuildRequest(service, "/tst/xxxx/111", []interface{}{map[string]string{"a": "a"}})
	var reply []byte
	meshClient.BaseCall(request, &reply)
	assert.Equal(t, "/2/tst/xxxx/111?a=a", string(reply))
	request.SetAttachment(mhttp.QueryString, "b=b")
	request.SetAttachment(mhttp.Method, "POST")
	meshClient.BaseCall(request, &reply)
	assert.Equal(t, "/2/tst/xxxx/111?b=b", string(reply))

	wg := &sync.WaitGroup{}
	wg.Add(goNum)
	requests := make(chan int, requestNum)
	for i := 0; i < goNum; i++ {
		go func() {
			defer wg.Done()
			for req := range requests {
				suffix := "test" + strconv.Itoa(req)
				request := meshClient.BuildRequest(service, "/tst/test", []interface{}{map[string]string{"index": suffix}})
				request.SetAttachment(mhttp.Method, "GET")
				var reply []byte
				meshClient.BaseCall(request, &reply)
				if !strings.HasSuffix(string(reply), suffix) {
					t.Errorf("wrong response")
				}
			}
		}()
	}
	for i := 0; i < requestNum; i++ {
		requests <- i
	}
	close(requests)
	wg.Wait()

}

func TestLocalEndpoint(t *testing.T) {
	var reply string
	meshClient.Call("LocalTestService", "hello", []interface{}{"service"}, &reply)
	assert.Equal(t, "hello service", reply)
}

func TestAgent_InitCall(t *testing.T) {
	//init
	agent := NewAgent(nil)
	agent.agentURL = &core.URL{Parameters: make(map[string]string)}
	urlTest := &core.URL{Parameters: make(map[string]string)}
	urlTest.Group = "test1"
	agent.initCluster(urlTest)
	agentHandler := &agentMessageHandler{agent: agent}

	//test init cluster with one path and one groups in clusterMap
	temp := agent.clusterMap.LoadOrNil(getClusterKey("test1", "0.1", "", ""))
	assert.NotNil(t, temp, "init cluster with one path and two groups in clusterMap fail")

	//init cluster with one path and one group in clusterMapWithoutGroup
	temp = agent.clusterMapWithoutGroup.LoadOrNil(getClusterKey("", "0.1", "", ""))
	assert.NotNil(t, "init cluster with one path and one group in clusterMapWithoutGroup fail")

	//test agentHandler call with group
	request := &core.MotanRequest{Attachment: core.NewStringMap(10)}
	request.SetAttachment(mpro.MGroup, "test1")
	ret := agentHandler.Call(request)
	assert.True(t, strings.HasPrefix(ret.GetException().ErrMsg, "No refers for request"))

	//test agentHandler call without group
	request.SetAttachment(mpro.MGroup, "")
	ret = agentHandler.Call(request)
	assert.True(t, strings.HasPrefix(ret.GetException().ErrMsg, "No refers for request"))

	//init cluster with one path and two groups in clusterMapWithoutGroup
	urlTest.Group = "test2"
	agent.initCluster(urlTest)
	temp = agent.clusterMapWithoutGroup.LoadOrNil(getClusterKey("", "0.1", "", ""))
	assert.Nil(t, temp, "init cluster with one path and two groups in clusterMapWithoutGroup fail")

	//test agentHandler call without group
	request.SetAttachment(mpro.MGroup, "")
	ret = agentHandler.Call(request)
	assert.True(t, strings.HasPrefix(ret.GetException().ErrMsg, "empty group is not supported"))
}

type LocalTestServiceProvider struct {
	url *core.URL
}

func (l *LocalTestServiceProvider) SetService(s interface{}) {
}

func (l *LocalTestServiceProvider) GetURL() *core.URL {
	return l.url
}

func (l *LocalTestServiceProvider) SetURL(url *core.URL) {
	l.url = url
}

func (l *LocalTestServiceProvider) IsAvailable() bool {
	return true
}

func (l *LocalTestServiceProvider) Call(request core.Request) core.Response {
	var requestStr string
	err := request.ProcessDeserializable([]interface{}{&requestStr})
	if err != nil {
		return core.BuildExceptionResponse(request.GetRequestID(), &core.Exception{
			ErrCode: 500,
			ErrMsg:  err.Error(),
			ErrType: core.ServiceException,
		})
	}
	return &core.MotanResponse{
		RequestID:   request.GetRequestID(),
		Value:       request.GetMethod() + " " + requestStr,
		Exception:   nil,
		ProcessTime: 0,
		Attachment:  nil,
		RPCContext:  nil,
	}
}

func (l *LocalTestServiceProvider) Destroy() {
}

func (l *LocalTestServiceProvider) GetPath() string {
	return l.url.Path
}
