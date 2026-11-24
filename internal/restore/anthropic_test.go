package restore_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker-agent/pkg/chat"

	"github.com/manzanit0/athenaeum/internal/restore"
)

func TestAnthropicAsksForRoomToSaveSeveralPages(t *testing.T) {
	asked := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		select {
		case asked <- body:
		default:
		}
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"stand-in"}}`, http.StatusBadRequest)
	}))
	defer server.Close()
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	model, err := restore.Anthropic(context.Background(), "sk-ant-test", restore.DefaultModel)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := model.CreateChatCompletionStream(context.Background(),
		[]chat.Message{{Role: chat.MessageRoleUser, Content: "Read page 1."}}, nil)
	if err == nil {
		_, _ = stream.Recv()
		stream.Close()
	}
	select {
	case body := <-asked:
		if limit, _ := body["max_tokens"].(float64); limit != 32000 {
			t.Errorf("max_tokens = %v, want 32000: a smaller limit cuts answers that save pages off", body["max_tokens"])
		}
		if name, _ := body["model"].(string); name != restore.DefaultModel {
			t.Errorf("model = %q, want %q", name, restore.DefaultModel)
		}
	default:
		t.Fatal("the model was never asked")
	}
}
