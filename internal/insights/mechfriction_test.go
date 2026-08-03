package insights

import (
	"strings"
	"testing"
)

// Bodies are verbatim from the frozen corpus (manifest 9eb7411f2fd2) — the
// classifier is corpus-tuned by design (spec honesty note I6); a signature
// change must break this table.
func TestClassifyMechanicalError(t *testing.T) {
	cases := []struct {
		body string
		mode string
		ok   bool
	}{
		{"<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>", modeEditBeforeRead, true},
		{"File does not exist. Note: your current working directory is /Users/dev/Developer/alpha/.worktrees/preview-issues.", modeWrongCwd, true},
		{"Exit code 1\n(eval):cd:1: no such file or directory: src", modeWrongCwd, true},
		{"Exit code 1\npattern ./...: directory prefix . does not contain main module or its selected dependencies", modeWrongCwd, true},
		{"Exit code 1\ngo: cannot find main module, but found .git/config in /Users/dev/Developer/alpha", modeWrongCwd, true},
		{"Refusing to write through symlink: /Users/dev/.config/alpha/config.yaml. Resolve the symlink and pass the real target path explicitly.", modeSymlinkEdit, true},
		{"<tool_use_error>String to replace not found in file.</tool_use_error>", "", false},
		{"<tool_use_error>File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.</tool_use_error>", "", false},
		{"Exit code 1", "", false},
		{"claude-opus-4-8[1m] is temporarily unavailable, so auto mode cannot determine the safety of Bash right now.", "", false},
	}
	for _, c := range cases {
		mode, ok := classifyMechanicalError(c.body)
		if ok != c.ok || mode != c.mode {
			t.Errorf("classify(%.50q) = (%q, %v), want (%q, %v)", c.body, mode, ok, c.mode, c.ok)
		}
	}
}

func TestErrorSignature(t *testing.T) {
	cases := []struct{ body, want string }{
		{"Exit code 1\nmake: *** [build] Error 2", "Exit code N"},
		{"Exit code 143\nlong tail", "Exit code N"},
		{"<tool_use_error>String to replace not found in file.\ndetail</tool_use_error>", "String to replace not found in file."},
	}
	for _, c := range cases {
		if got := errorSignature(c.body); got != c.want {
			t.Errorf("errorSignature(%.40q) = %q, want %q", c.body, got, c.want)
		}
	}
	long := strings.Repeat("x", 300)
	if n := len([]rune(errorSignature(long))); n > 120 {
		t.Errorf("signature length = %d, want <= 120", n)
	}
}

func TestSanitizeEvidenceText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"File does not exist. Note: your current working directory is /Users/dev/Developer/alpha/.worktrees/preview-issues.",
			"File does not exist. Note: your current working directory is [path]"},
		{"Refusing to write through symlink: /Users/dev/.config/alpha/config.yaml. Resolve the symlink and pass the real target path explicitly.",
			"Refusing to write through symlink: [path] Resolve the symlink and pass the real target path explicitly."},
		{"<tool_use_error>File has not been read yet. Read it first before writing to it.</tool_use_error>",
			"File has not been read yet. Read it first before writing to it."},
		{"resume session 8f3d2a1b-4c5d-6e7f-8a9b-0c1d2e3f4a5b please", "resume session [id] please"},
		{"fix sc-42 first", "fix [ticket] first"},
		{"tail -f /dev/null stays intact", "tail -f /dev/null stays intact"},
		{"echo hi > /tmp/alpha_perm_test.txt — and nothing else", "echo hi > [path] — and nothing else"},
		{"set $HOME/.config first", "set [path] first"},
	}
	for _, c := range cases {
		if got := SanitizeEvidenceText(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
