package signals

import "testing"

func TestInterpret(t *testing.T) {
	cases := []struct {
		code       int
		wantNil    bool
		wantSignal string
		wantNo     int
	}{
		{0, true, "", 0},
		{1, true, "", 0},   // plain app failure, not a signal
		{128, true, "", 0}, // boundary: not 128+N
		{130, false, "SIGINT", 2},
		{137, false, "SIGKILL", 9},
		{143, false, "SIGTERM", 15},
		{139, false, "SIGSEGV", 11},
		{134, false, "SIGABRT", 6},
		{200, true, "", 0}, // above the signal range
	}
	for _, c := range cases {
		got := Interpret(c.code)
		if c.wantNil {
			if got != nil {
				t.Errorf("Interpret(%d) = %+v, want nil", c.code, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("Interpret(%d) = nil, want %s", c.code, c.wantSignal)
			continue
		}
		if got.Signal != c.wantSignal || got.SignalNo != c.wantNo || got.ExitCode != c.code {
			t.Errorf("Interpret(%d) = %+v, want signal=%s no=%d", c.code, got, c.wantSignal, c.wantNo)
		}
		if got.Meaning == "" {
			t.Errorf("Interpret(%d) has empty meaning", c.code)
		}
	}
}

func TestAnnotate(t *testing.T) {
	if got := Annotate(130); got != " (SIGINT)" {
		t.Errorf("Annotate(130) = %q, want %q", got, " (SIGINT)")
	}
	if got := Annotate(1); got != "" {
		t.Errorf("Annotate(1) = %q, want empty", got)
	}
}
