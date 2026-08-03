package insights

import (
	"sort"
	"strings"
	"time"

	"tmux-ctrl/internal/transcript"
)

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// statsBuilder accumulates AgentSessionStats across one pass over the decoded events.
type statsBuilder struct {
	sessionID string
	repo      RepoResolver

	cwd       string
	gitBranch string
	aiTitle   string
	version   string

	start    time.Time
	end      time.Time
	startSet bool

	seenMsg        map[string]bool
	assistantTurns int
	modelMix       map[string]int
	tokens         TokenUsage

	toolCounts     map[string]int
	subagentFanout int
	subagentSet    map[string]bool
	skillsSet      map[string]bool
	pluginsSet     map[string]bool
	edits          int
	writes         int
	linesAdded     int
	linesRemoved   int
	filesSet       map[string]bool

	toolErrors        int
	rejections        int
	interrupts        int
	userTurns         int
	taskNotifications int

	userTurnFingerprints []string

	mechFriction    map[string]int
	mechExemplars   map[string]string
	otherSignatures map[string]int
	directiveAgg    map[string]*DirectiveClause
}

func newStatsBuilder(sessionID string, repo RepoResolver) *statsBuilder {
	return &statsBuilder{
		sessionID:       sessionID,
		repo:            repo,
		seenMsg:         map[string]bool{},
		modelMix:        map[string]int{},
		toolCounts:      map[string]int{},
		subagentSet:     map[string]bool{},
		skillsSet:       map[string]bool{},
		pluginsSet:      map[string]bool{},
		filesSet:        map[string]bool{},
		mechFriction:    map[string]int{},
		mechExemplars:   map[string]string{},
		otherSignatures: map[string]int{},
		directiveAgg:    map[string]*DirectiveClause{},
	}
}

func (b *statsBuilder) add(ev transcript.TranscriptEvent) {
	if ev.Cwd != "" {
		b.cwd = ev.Cwd
	}
	if ev.GitBranch != "" {
		b.gitBranch = ev.GitBranch
	}
	if ev.AiTitle != "" {
		b.aiTitle = ev.AiTitle
	}
	if ev.Version != "" {
		b.version = ev.Version
	}
	if !ev.Timestamp.IsZero() {
		if !b.startSet {
			b.start = ev.Timestamp
			b.startSet = true
		}
		b.end = ev.Timestamp
	}

	if ev.AttributionSkill != "" {
		b.skillsSet[ev.AttributionSkill] = true
	}
	if ev.AttributionPlugin != "" {
		b.pluginsSet[ev.AttributionPlugin] = true
	}

	if ev.Type == "assistant" && ev.Message != nil {
		b.addAssistantMessage(ev.Message)
		// Tool counts are per-block across ALL lines (a message split across lines
		// carries one block per line), so they are NOT deduped by message.id.
		for _, blk := range ev.Message.Content {
			if blk.Type == "tool_use" {
				b.countTool(blk)
			}
		}
	}

	if ev.Type == "user" && ev.Message != nil {
		b.addUserEvent(ev.Message)
	}

	if ev.ToolUseResult != nil && len(ev.ToolUseResult.StructuredPatch) > 0 {
		if ev.ToolUseResult.FilePath != "" {
			b.filesSet[ev.ToolUseResult.FilePath] = true
		}
		for _, h := range ev.ToolUseResult.StructuredPatch {
			for _, ln := range h.Lines {
				switch {
				case strings.HasPrefix(ln, "+"):
					b.linesAdded++
				case strings.HasPrefix(ln, "-"):
					b.linesRemoved++
				}
			}
		}
	}
}

// addUserEvent classifies a user line's blocks. Friction lives here: is_error
// tool_results split into interrupt / rejection / genuine error; interrupt text
// blocks (is_error-absent) counted too; real user prose counted once, excluding
// synthetic/injected content, task-notifications, rejections, and interrupts.
func (b *statsBuilder) addUserEvent(m *transcript.Message) {
	var textParts []string
	for _, blk := range m.Content {
		switch blk.Type {
		case "tool_result":
			body := blk.ToolResult
			if blk.HasIsError && blk.IsError {
				switch {
				case isInterruptText(body):
					b.interrupts++
				case isRejectionText(body):
					b.rejections++
					b.classifyRejection(body)
				default:
					b.toolErrors++
					b.classifyToolError(body)
				}
			} else if isInterruptText(body) {
				b.interrupts++
			}
		case "text":
			textParts = append(textParts, blk.Text)
		}
	}
	joined := strings.TrimSpace(strings.Join(textParts, "\n"))
	if joined == "" {
		return
	}
	switch {
	case isTaskNotification(joined):
		b.taskNotifications++
	case isInterruptText(joined):
		b.interrupts++
	case isRejectionText(joined):
		b.rejections++
		b.classifyRejection(joined)
	case isSyntheticUserText(joined):
		// injected pseudo-user content: dropped
	default:
		b.userTurns++
		if norm := normalizeFingerprintText(joined); !isTrivialTurn(norm) {
			b.userTurnFingerprints = append(b.userTurnFingerprints, fingerprint(norm))
		}
		first := b.userTurns == 1
		for _, clause := range extractClauses(joined) {
			norm := normalizeClause(clause)
			dc, ok := b.directiveAgg[norm]
			if !ok {
				dc = &DirectiveClause{Norm: norm, Exemplar: clause}
				b.directiveAgg[norm] = dc
			}
			dc.Count++
			if first {
				dc.FirstTurn++
			}
		}
	}
}

// classifyRejection counts only reason-less rejections as mechanical
// permission friction — a rejection carrying an inline user correction is
// deliberate steering, not friction. No exemplar: the body is boilerplate.
func (b *statsBuilder) classifyRejection(body string) {
	if _, reasoned := rejectionReason(body); !reasoned {
		b.mechFriction[modePermission]++
	}
}

func (b *statsBuilder) classifyToolError(body string) {
	mode, ok := classifyMechanicalError(body)
	if !ok {
		b.mechFriction[modeOther]++
		b.otherSignatures[errorSignature(body)]++
		return
	}
	b.mechFriction[mode]++
	if _, seen := b.mechExemplars[mode]; !seen {
		b.mechExemplars[mode] = SanitizeEvidenceText(body)
	}
}

func (b *statsBuilder) countTool(blk transcript.ContentBlock) {
	if blk.ToolName == "" {
		return
	}
	b.toolCounts[blk.ToolName]++
	switch blk.ToolName {
	case "Agent":
		b.subagentFanout++
		if st, ok := blk.ToolInput["subagent_type"].(string); ok && st != "" {
			b.subagentSet[st] = true
		}
	case "Edit":
		b.edits++
	case "Write":
		b.writes++
	}
}

// addAssistantMessage accumulates per-message stats once per distinct message.id.
// One assistant message spans multiple JSONL lines (one per content block) with
// identical usage, so de-duping by id is required to avoid ~2.6-3.5x inflation.
func (b *statsBuilder) addAssistantMessage(m *transcript.Message) {
	if m.ID != "" && b.seenMsg[m.ID] {
		return
	}
	if m.ID != "" {
		b.seenMsg[m.ID] = true
	}
	b.assistantTurns++
	if m.Model != "" {
		b.modelMix[m.Model]++
	}
	if m.Usage != nil {
		b.tokens.Input += m.Usage.Input
		b.tokens.Output += m.Usage.Output
		b.tokens.CacheCreation += m.Usage.CacheCreation
		if m.Usage.CacheRead > b.tokens.CacheReadPeak {
			b.tokens.CacheReadPeak = m.Usage.CacheRead
		}
	}
}

func (b *statsBuilder) finish() AgentSessionStats {
	s := AgentSessionStats{
		SessionID: b.sessionID,
		Cwd:       b.cwd,
		GitBranch: b.gitBranch,
		AiTitle:   b.aiTitle,
		Version:   b.version,
		Start:     b.start,
		End:       b.end,
	}
	if b.repo != nil {
		s.Repo = b.repo(b.cwd)
	}
	if b.startSet {
		s.WallClock = b.end.Sub(b.start)
	}
	s.AssistantTurns = b.assistantTurns
	s.ModelMix = b.modelMix
	s.Tokens = b.tokens

	s.ToolCounts = b.toolCounts
	s.SubagentFanout = b.subagentFanout
	s.Subagents = sortedKeys(b.subagentSet)
	s.Skills = sortedKeys(b.skillsSet)
	s.Plugins = sortedKeys(b.pluginsSet)
	s.Edits = b.edits
	s.Writes = b.writes
	s.LinesAdded = b.linesAdded
	s.LinesRemoved = b.linesRemoved
	s.FilesTouched = sortedKeys(b.filesSet)

	s.ToolErrors = b.toolErrors
	s.Rejections = b.rejections
	s.Interrupts = b.interrupts
	s.UserTurns = b.userTurns
	s.TaskNotifications = b.taskNotifications
	s.UserTurnFingerprints = b.userTurnFingerprints
	if len(b.mechFriction) > 0 {
		s.MechanicalFriction = b.mechFriction
	}
	if len(b.mechExemplars) > 0 {
		s.MechanicalExemplars = b.mechExemplars
	}
	if len(b.otherSignatures) > 0 {
		s.OtherErrorSignatures = b.otherSignatures
	}
	if len(b.directiveAgg) > 0 {
		norms := make([]string, 0, len(b.directiveAgg))
		for n := range b.directiveAgg {
			norms = append(norms, n)
		}
		sort.Strings(norms)
		for _, n := range norms {
			s.DirectiveClauses = append(s.DirectiveClauses, *b.directiveAgg[n])
		}
	}
	return s
}
