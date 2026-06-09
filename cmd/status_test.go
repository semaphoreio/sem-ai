package cmd

import "testing"

func TestBucketForPipeline(t *testing.T) {
	cases := []struct {
		state, result, want string
	}{
		{"done", "passed", "passed"},
		{"done", "failed", "failed"},
		{"done", "stopped", "failed"},
		{"done", "canceled", "failed"},
		{"running", "", "pending"},
		{"running", "passed", "pending"}, // not terminal yet → pending regardless of result
		{"", "", "pending"},              // unknown/missing pipeline data
		{"done", "", "pending"},          // terminal but no result yet
	}
	for _, c := range cases {
		if got := bucketForPipeline(c.state, c.result); got != c.want {
			t.Errorf("bucketForPipeline(%q,%q)=%q, want %q", c.state, c.result, got, c.want)
		}
	}
}

func TestExitCodeForBucket(t *testing.T) {
	cases := []struct {
		bucket any
		want   int
	}{
		{"passed", exitPass},
		{"pending", exitPending},
		{"failed", exitFail},
		{"", exitFail}, // unknown bucket is treated as a failure (never falsely green)
		{nil, exitFail},
	}
	for _, c := range cases {
		if got := exitCodeForBucket(c.bucket); got != c.want {
			t.Errorf("exitCodeForBucket(%v)=%d, want %d", c.bucket, got, c.want)
		}
	}
}
