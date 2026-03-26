package mcpserver

import "testing"

func TestDisplayRepo(t *testing.T) {
	tests := []struct {
		name       string
		rawURL     string
		sourceType string
		want       string
	}{
		{
			name:       "empty URL",
			rawURL:     "",
			sourceType: "",
			want:       "",
		},
		{
			name:       "github URL",
			rawURL:     "https://github.com/owner/repo",
			sourceType: "git",
			want:       "owner/repo",
		},
		{
			name:       "github URL with .git suffix",
			rawURL:     "https://github.com/owner/repo.git",
			sourceType: "git",
			want:       "owner/repo",
		},
		{
			name:       "github URL with extra path segments",
			rawURL:     "https://github.com/owner/repo/tree/main/docs",
			sourceType: "git",
			want:       "owner/repo",
		},
		{
			name:       "gitlab URL",
			rawURL:     "https://gitlab.com/org/project",
			sourceType: "git",
			want:       "gitlab.com/org/project",
		},
		{
			name:       "gitlab subgroups",
			rawURL:     "https://gitlab.com/org/sub/project",
			sourceType: "git",
			want:       "gitlab.com/org/sub/project",
		},
		{
			name:       "enterprise host with port",
			rawURL:     "https://git.internal.com:8443/team/project",
			sourceType: "git",
			want:       "git.internal.com:8443/team/project",
		},
		{
			name:       "local unix path",
			rawURL:     "/home/user/docs",
			sourceType: "local",
			want:       "/home/user/docs",
		},
		{
			name:       "local windows path",
			rawURL:     "C:\\Users\\user\\docs",
			sourceType: "local",
			want:       "C:\\Users\\user\\docs",
		},
		{
			name:       "inferred local from slash prefix",
			rawURL:     "/usr/local/share/docs",
			sourceType: "",
			want:       "/usr/local/share/docs",
		},
		{
			name:       "inferred git from https prefix",
			rawURL:     "https://github.com/foo/bar",
			sourceType: "",
			want:       "foo/bar",
		},
		{
			name:       "inferred local from windows drive letter",
			rawURL:     "D:\\projects\\docs",
			sourceType: "",
			want:       "D:\\projects\\docs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DisplayRepo(tt.rawURL, tt.sourceType)
			if got != tt.want {
				t.Errorf("DisplayRepo(%q, %q) = %q, want %q", tt.rawURL, tt.sourceType, got, tt.want)
			}
		})
	}
}
