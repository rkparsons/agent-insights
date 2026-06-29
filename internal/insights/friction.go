package insights

import "strings"

// Friction markers, observed stable across the real corpus. Interrupts appear as
// is_error-absent text blocks; rejections are is_error:true tool_results bearing
// the rejection prefix (~48% with no inline reason).
const (
	interruptMarker = "[Request interrupted by user"
	rejectionPrefix = "The user doesn't want to proceed with this tool use"
	userSaidMarker  = "the user said:"
)

// metaPrefixes are injected pseudo-user content that must be stripped — they are
// not what the user actually said. <task-notification> is deliberately absent.
var metaPrefixes = []string{
	"<ide_opened_file", "<ide_selection", "<command-name", "<command-message",
	"<command-args", "<command-stdout", "<local-command-stdout", "<local-command-caveat",
	"<system-reminder", "<bash-", "<user-prompt-submit", "Caveat: The messages below",
	"Base directory for this skill:", "Result of calling the", "The following deferred tools",
}

func isInterruptText(s string) bool { return strings.Contains(s, interruptMarker) }

func isRejectionText(s string) bool { return strings.Contains(s, rejectionPrefix) }

// rejectionReason returns the user's correction after "the user said:", or
// ("", false) for the ~48% of rejections with no inline reason.
func rejectionReason(s string) (string, bool) {
	if _, after, found := strings.Cut(s, userSaidMarker); found {
		return strings.TrimSpace(after), true
	}
	return "", false
}

func isSyntheticUserText(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	for _, p := range metaPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func isTaskNotification(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "<task-notification>")
}
