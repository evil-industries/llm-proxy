package management

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementevents"
)

func TestEventsStreamsChangesAndStopsOnShutdown(t *testing.T) {
	h := &Handler{}
	router := gin.New()
	router.GET("/events", h.Events)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := response.Body.Close(); errClose != nil {
			t.Error(errClose)
		}
	}()
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != ": connected\n" {
		t.Fatalf("initial stream: %q, %v", line, err)
	}
	managementevents.Publish(managementevents.Accounts)
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			break
		}
	}
	if line != "data: 1\n" {
		t.Fatalf("event = %q", line)
	}
	h.CloseEventStreams()
	h.CloseEventStreams()
	if _, errRead := io.ReadAll(reader); errRead != nil {
		t.Fatal(errRead)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/events", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status after close = %d", recorder.Code)
	}
}
