package s3api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/filer/leveldb2"
	"GoBlob/goblob/server"
)

type testS3Env struct {
	httpServer *httptest.Server
	s3         *S3ApiServer
	grpcServer *grpc.Server
	grpcLis    net.Listener
	store      *leveldb2.LevelDB2Store
	filerAddr  types.ServerAddress
}

func setupS3Env(t *testing.T) *testS3Env {
	t.Helper()

	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(64<<20), grpc.MaxSendMsgSize(64<<20))
	fs, err := server.NewFilerServer(http.NewServeMux(), http.NewServeMux(), grpcServer, server.DefaultFilerOption())
	if err != nil {
		t.Fatalf("NewFilerServer: %v", err)
	}

	store, err := leveldb2.NewLevelDB2Store(t.TempDir())
	if err != nil {
		t.Fatalf("NewLevelDB2Store: %v", err)
	}
	fs.SetStore(store)

	go func() { _ = grpcServer.Serve(grpcLis) }()

	grpcPort := grpcLis.Addr().(*net.TCPAddr).Port
	filerAddr := types.ServerAddress(fmt.Sprintf("127.0.0.1:8888.%d", grpcPort))

	env := &testS3Env{
		grpcServer: grpcServer,
		grpcLis:    grpcLis,
		store:      store,
		filerAddr:  filerAddr,
	}
	env.startS3(t)
	t.Cleanup(func() {
		if env.httpServer != nil {
			env.httpServer.Close()
		}
		if env.s3 != nil {
			env.s3.Shutdown()
		}
		grpcServer.Stop()
		_ = grpcLis.Close()
		store.Shutdown()
	})
	return env
}

func (env *testS3Env) startS3(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	opt := DefaultS3ApiServerOption()
	opt.Filers = []types.ServerAddress{env.filerAddr}
	s3, err := NewS3ApiServer(mux, opt)
	if err != nil {
		t.Fatalf("NewS3ApiServer: %v", err)
	}
	httpServer := httptest.NewServer(mux)
	env.s3 = s3
	env.httpServer = httpServer
}

func (env *testS3Env) restartS3(t *testing.T) {
	t.Helper()
	if env.httpServer != nil {
		env.httpServer.Close()
	}
	if env.s3 != nil {
		env.s3.Shutdown()
	}
	env.startS3(t)
}

func TestS3BucketAndObjectOperations(t *testing.T) {
	env := setupS3Env(t)
	client := env.httpServer.Client()

	req, _ := http.NewRequest(http.MethodPut, env.httpServer.URL+"/test-bucket", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create bucket request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create bucket status=%d body=%s", resp.StatusCode, string(body))
	}
	_ = resp.Body.Close()

	putBody := []byte("hello goblob")
	req, _ = http.NewRequest(http.MethodPut, env.httpServer.URL+"/test-bucket/hello.txt", bytes.NewReader(putBody))
	req.Header.Set("Content-Type", "text/plain")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("put object failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("put object status=%d body=%s", resp.StatusCode, string(body))
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("put object missing ETag")
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/test-bucket/hello.txt", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("get object failed: %v", err)
	}
	gotBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(gotBody) != string(putBody) {
		t.Fatalf("get object status=%d body=%q", resp.StatusCode, string(gotBody))
	}

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/test-bucket", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("list objects failed: %v", err)
	}
	listXML, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(listXML), "hello.txt") {
		t.Fatalf("list objects status=%d body=%s", resp.StatusCode, string(listXML))
	}

	req, _ = http.NewRequest(http.MethodDelete, env.httpServer.URL+"/test-bucket/hello.txt", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete object failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete object status=%d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodDelete, env.httpServer.URL+"/test-bucket", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete bucket failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete bucket status=%d", resp.StatusCode)
	}
}

func TestS3MultipartFlow(t *testing.T) {
	env := setupS3Env(t)
	client := env.httpServer.Client()

	req, _ := http.NewRequest(http.MethodPut, env.httpServer.URL+"/mp-bucket", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodPost, env.httpServer.URL+"/mp-bucket/large.bin?uploads", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("init multipart failed: %v", err)
	}
	initBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init multipart status=%d body=%s", resp.StatusCode, string(initBody))
	}

	var initResp initiateMultipartUploadResult
	if err := xml.Unmarshal(initBody, &initResp); err != nil {
		t.Fatalf("unmarshal initiate multipart xml: %v", err)
	}
	if initResp.UploadID == "" {
		t.Fatalf("upload id should not be empty")
	}

	part1 := bytes.Repeat([]byte("a"), minMultipartPartSize)
	req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("%s/mp-bucket/large.bin?partNumber=1&uploadId=%s", env.httpServer.URL, initResp.UploadID), bytes.NewReader(part1))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("upload part1 failed: %v", err)
	}
	part1ETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || part1ETag == "" {
		t.Fatalf("upload part1 status=%d etag=%q", resp.StatusCode, part1ETag)
	}

	part2 := []byte("tail")
	req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("%s/mp-bucket/large.bin?partNumber=2&uploadId=%s", env.httpServer.URL, initResp.UploadID), bytes.NewReader(part2))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("upload part2 failed: %v", err)
	}
	part2ETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || part2ETag == "" {
		t.Fatalf("upload part2 status=%d etag=%q", resp.StatusCode, part2ETag)
	}

	completeXML := fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part><Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, part1ETag, part2ETag)
	req, _ = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/mp-bucket/large.bin?uploadId=%s", env.httpServer.URL, initResp.UploadID), strings.NewReader(completeXML))
	req.Header.Set("Content-Type", "application/xml")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("complete multipart failed: %v", err)
	}
	completeBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete multipart status=%d body=%s", resp.StatusCode, string(completeBody))
	}

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/mp-bucket/large.bin", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("get multipart object failed: %v", err)
	}
	fullBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get multipart object status=%d", resp.StatusCode)
	}
	if len(fullBody) != len(part1)+len(part2) {
		t.Fatalf("multipart object size=%d want=%d", len(fullBody), len(part1)+len(part2))
	}
}

func TestS3MultipartSurvivesRestart(t *testing.T) {
	env := setupS3Env(t)
	client := env.httpServer.Client()

	req, _ := http.NewRequest(http.MethodPut, env.httpServer.URL+"/restart-bucket", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodPost, env.httpServer.URL+"/restart-bucket/object.bin?uploads", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("init multipart failed: %v", err)
	}
	initBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init multipart status=%d body=%s", resp.StatusCode, string(initBody))
	}
	var initResp initiateMultipartUploadResult
	if err := xml.Unmarshal(initBody, &initResp); err != nil {
		t.Fatalf("unmarshal initiate multipart xml: %v", err)
	}
	if initResp.UploadID == "" {
		t.Fatalf("upload id should not be empty")
	}

	part1 := bytes.Repeat([]byte("x"), minMultipartPartSize)
	req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("%s/restart-bucket/object.bin?partNumber=1&uploadId=%s", env.httpServer.URL, initResp.UploadID), bytes.NewReader(part1))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("upload part1 failed: %v", err)
	}
	part1ETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || part1ETag == "" {
		t.Fatalf("upload part1 status=%d etag=%q", resp.StatusCode, part1ETag)
	}

	env.restartS3(t)
	client = env.httpServer.Client()

	part2 := []byte("after-restart")
	req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("%s/restart-bucket/object.bin?partNumber=2&uploadId=%s", env.httpServer.URL, initResp.UploadID), bytes.NewReader(part2))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("upload part2 after restart failed: %v", err)
	}
	part2ETag := resp.Header.Get("ETag")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || part2ETag == "" {
		t.Fatalf("upload part2 status=%d etag=%q", resp.StatusCode, part2ETag)
	}

	completeXML := fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part><Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, part1ETag, part2ETag)
	req, _ = http.NewRequest(http.MethodPost, fmt.Sprintf("%s/restart-bucket/object.bin?uploadId=%s", env.httpServer.URL, initResp.UploadID), strings.NewReader(completeXML))
	req.Header.Set("Content-Type", "application/xml")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("complete multipart after restart failed: %v", err)
	}
	completeBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete multipart status=%d body=%s", resp.StatusCode, string(completeBody))
	}

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/restart-bucket/object.bin", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("get object after completion failed: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get object status=%d", resp.StatusCode)
	}
	if len(data) != len(part1)+len(part2) {
		t.Fatalf("object size=%d want=%d", len(data), len(part1)+len(part2))
	}
}

func TestS3AdvancedFeatures(t *testing.T) {
	env := setupS3Env(t)
	client := env.httpServer.Client()

	req, _ := http.NewRequest(http.MethodPut, env.httpServer.URL+"/adv-bucket", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	_ = resp.Body.Close()

	versioningXML := `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>`
	req, _ = http.NewRequest(http.MethodPut, env.httpServer.URL+"/adv-bucket?versioning", strings.NewReader(versioningXML))
	req.Header.Set("Content-Type", "application/xml")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("put versioning failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put versioning status=%d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPut, env.httpServer.URL+"/adv-bucket/secret.txt", strings.NewReader("secret-data"))
	req.Header.Set("x-amz-server-side-encryption", "AES256")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("put object with sse failed: %v", err)
	}
	if resp.Header.Get("x-amz-version-id") == "" {
		t.Fatalf("expected x-amz-version-id header")
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/adv-bucket/secret.txt", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("get object failed: %v", err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("x-amz-server-side-encryption"); got != "AES256" {
		t.Fatalf("expected AES256 header, got %q", got)
	}

	policy := `{"Version":"2012-10-17","Statement":[]}`
	req, _ = http.NewRequest(http.MethodPut, env.httpServer.URL+"/adv-bucket?policy", strings.NewReader(policy))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("put policy failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put policy status=%d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/adv-bucket?policy", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("get policy failed: %v", err)
	}
	policyBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(policyBody), "2012-10-17") {
		t.Fatalf("get policy status=%d body=%s", resp.StatusCode, string(policyBody))
	}

	corsXML := `<CORSConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><CORSRule><AllowedOrigin>https://example.com</AllowedOrigin><AllowedMethod>GET</AllowedMethod><AllowedHeader>*</AllowedHeader></CORSRule></CORSConfiguration>`
	req, _ = http.NewRequest(http.MethodPut, env.httpServer.URL+"/adv-bucket?cors", strings.NewReader(corsXML))
	req.Header.Set("Content-Type", "application/xml")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("put cors failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put cors status=%d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodOptions, env.httpServer.URL+"/adv-bucket/secret.txt", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("cors preflight failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cors preflight status=%d", resp.StatusCode)
	}

	taggingXML := `<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><TagSet><Tag><Key>env</Key><Value>dev</Value></Tag></TagSet></Tagging>`
	req, _ = http.NewRequest(http.MethodPut, env.httpServer.URL+"/adv-bucket/secret.txt?tagging", strings.NewReader(taggingXML))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("put tagging failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put tagging status=%d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, env.httpServer.URL+"/adv-bucket/secret.txt?tagging", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("get tagging failed: %v", err)
	}
	tagBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(tagBody), "<Key>env</Key>") {
		t.Fatalf("get tagging status=%d body=%s", resp.StatusCode, string(tagBody))
	}
}
