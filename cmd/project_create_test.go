package cmd

import "testing"

func TestProjectNameFromURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"git@github.com:renderedtext/architecture.git", "architecture", false},
		{"git@github.com:renderedtext/architecture", "architecture", false},
		{"https://github.com/renderedtext/architecture.git", "architecture", false},
		{"https://github.com/renderedtext/architecture", "architecture", false},
		{"git@gitlab.com:group/sub/repo.git", "repo", false},
		{"https://bitbucket.org/team/my-repo.git", "my-repo", false},
		{"not-a-url", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := projectNameFromURL(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%q: want error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
