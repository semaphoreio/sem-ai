package cmd

import (
	"strings"
	"testing"
)

func TestDebugDisabledMsg(t *testing.T) {
	msg := debugDisabledMsg("my-app", nil)

	if !strings.Contains(msg, "my-app") {
		t.Errorf("message should name the project, got: %q", msg)
	}
	if !strings.Contains(msg, "Collaborators can start empty debug sessions") {
		t.Errorf("message should point at the exact setting to enable, got: %q", msg)
	}
	if strings.Contains(msg, "Server response") {
		t.Errorf("message should omit the server-response line when body is empty, got: %q", msg)
	}
}

func TestDebugDisabledMsgWithBody(t *testing.T) {
	body := []byte(`{"code":7,"message":"You are not allowed to debug this project"}`)
	msg := debugDisabledMsg("my-app", body)

	if !strings.Contains(msg, "Server response") {
		t.Errorf("message should include the server response when body is present, got: %q", msg)
	}
	if !strings.Contains(msg, "not allowed to debug this project") {
		t.Errorf("message should surface the raw server body, got: %q", msg)
	}
}
