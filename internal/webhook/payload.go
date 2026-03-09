package webhook

// PushEvent represents a GitHub push webhook payload.
type PushEvent struct {
	Ref        string     `json:"ref"`
	Before     string     `json:"before"`
	After      string     `json:"after"`
	Repository Repository `json:"repository"`
	Pusher     Pusher     `json:"pusher"`
	HeadCommit Commit     `json:"head_commit"`
	Commits    []Commit   `json:"commits"`
}

// Branch returns the short branch name from the ref (e.g. "main" from "refs/heads/main").
func (e *PushEvent) Branch() string {
	if len(e.Ref) > len("refs/heads/") {
		return e.Ref[len("refs/heads/"):]
	}
	return e.Ref
}

// IsTag returns true if the push event is for a tag.
func (e *PushEvent) IsTag() bool {
	return len(e.Ref) > len("refs/tags/") && e.Ref[:len("refs/tags/")] == "refs/tags/"
}

// PullRequestEvent represents a GitHub pull_request webhook payload.
type PullRequestEvent struct {
	Action      string      `json:"action"`
	Number      int         `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
	Sender      Sender      `json:"sender"`
}

// PullRequest contains PR-specific fields.
type PullRequest struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	Head    PRRef  `json:"head"`
	Base    PRRef  `json:"base"`
	User    Sender `json:"user"`
}

// PRRef represents a branch reference within a PR.
type PRRef struct {
	Label string     `json:"label"`
	Ref   string     `json:"ref"`
	SHA   string     `json:"sha"`
	Repo  Repository `json:"repo"`
}

// Repository contains repository metadata.
type Repository struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	CloneURL string `json:"clone_url"`
	Private  bool   `json:"private"`
}

// Commit represents a single commit in the push event.
type Commit struct {
	ID        string   `json:"id"`
	Message   string   `json:"message"`
	Timestamp string   `json:"timestamp"`
	URL       string   `json:"url"`
	Author    Author   `json:"author"`
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Modified  []string `json:"modified"`
}

// Author contains commit author information.
type Author struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

// Pusher is the user who pushed the commits.
type Pusher struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Sender is the GitHub user who triggered the event.
type Sender struct {
	Login string `json:"login"`
	ID    int    `json:"id"`
}
