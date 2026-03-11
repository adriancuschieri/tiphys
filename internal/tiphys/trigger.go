package tiphys

import (
	"path/filepath"
	"strings"
)

// EventType identifies which kind of GitHub event is being processed.
type EventType string

const (
	EventPush        EventType = "push"
	EventPullRequest EventType = "pull_request"
	EventTag         EventType = "tag"
)

// Event carries the normalised fields the trigger matcher needs.
type Event struct {
	Type     EventType
	Ref      string // full git ref, e.g. "refs/heads/main"
	Branch   string // short branch name for push/PR, e.g. "main"
	Tag      string // short tag name for tag events, e.g. "v1.2.3"
	PRAction string // "opened", "reopened", "synchronize" (PR events only)
	PRBase   string // target branch of the PR
}

// ShouldTrigger returns true if the event matches any rule in the TriggerConfig.
func ShouldTrigger(cfg TriggerConfig, event Event) bool {
	switch event.Type {
	case EventPush:
		return matchPush(cfg.Push, event)
	case EventPullRequest:
		return matchPR(cfg.PullRequest, event)
	case EventTag:
		return matchTag(cfg.Tag, event)
	}
	return false
}

func matchPush(t *PushTrigger, event Event) bool {
	if t == nil {
		return false
	}
	// Check ignore list first
	for _, pattern := range t.Ignore {
		if globMatch(pattern, event.Branch) {
			return false
		}
	}
	// Empty branches list means match all
	if len(t.Branches) == 0 {
		return true
	}
	for _, pattern := range t.Branches {
		if globMatch(pattern, event.Branch) {
			return true
		}
	}
	return false
}

func matchPR(t *PullRequestTrigger, event Event) bool {
	if t == nil {
		return false
	}
	// Check action filter
	actions := t.Actions
	if len(actions) == 0 {
		actions = []string{"opened", "reopened", "synchronize"}
	}
	actionMatched := false
	for _, a := range actions {
		if strings.EqualFold(a, event.PRAction) {
			actionMatched = true
			break
		}
	}
	if !actionMatched {
		return false
	}
	// Empty branches means match all target branches
	if len(t.Branches) == 0 {
		return true
	}
	for _, pattern := range t.Branches {
		if globMatch(pattern, event.PRBase) {
			return true
		}
	}
	return false
}

func matchTag(t *TagTrigger, event Event) bool {
	if t == nil {
		return false
	}
	if t.Pattern == "" {
		return true // match all tags
	}
	return globMatch(t.Pattern, event.Tag)
}

// globMatch wraps filepath.Match so callers don't need to handle the error
// (only returned on malformed patterns, which we surface as no-match).
func globMatch(pattern, value string) bool {
	matched, err := filepath.Match(pattern, value)
	if err != nil {
		return false
	}
	return matched
}
