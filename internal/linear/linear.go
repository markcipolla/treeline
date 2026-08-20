// Package linear is a minimal Linear GraphQL client plus the OAuth flow.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiURL = "https://api.linear.app/graphql"

type Issue struct {
	ID          string
	Identifier  string // e.g. LMAP-142
	Title       string
	Description string // markdown body, may be empty
	Priority    int    // 0 none, 1 urgent, 2 high, 3 medium, 4 low
	URL         string
	State       string
	StateType   string
	Assignee    string // display name, empty when unassigned
	Labels      []string
}

type Client struct {
	token string
	http  *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

type issueNode struct {
	ID          string  `json:"id"`
	Identifier  string  `json:"identifier"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Priority    float64 `json:"priority"`
	URL         string  `json:"url"`
	State       struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Assignee *struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"assignee"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

func (n issueNode) toIssue() Issue {
	labels := make([]string, 0, len(n.Labels.Nodes))
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	assignee := ""
	if n.Assignee != nil {
		assignee = assigneeLabel(n.Assignee.Name, n.Assignee.DisplayName)
	}
	return Issue{
		ID:          n.ID,
		Identifier:  n.Identifier,
		Title:       n.Title,
		Description: n.Description,
		Priority:    int(n.Priority),
		URL:         n.URL,
		State:       n.State.Name,
		StateType:   n.State.Type,
		Assignee:    assignee,
		Labels:      labels,
	}
}

// assigneeLabel picks the shortest human label for an assignee. Linear leaves
// name set to the member's email address until they set a real one, which is
// both long and noisy in a table column; displayName is always the short
// handle ("mark.cipolla"), so it stands in for an unset name.
func assigneeLabel(name, displayName string) string {
	if name != "" && !strings.Contains(name, "@") {
		return name
	}
	if displayName != "" {
		return displayName
	}
	if i := strings.Index(name, "@"); i > 0 {
		return name[:i] // no displayName either: drop the domain at least
	}
	return name
}

func toIssues(nodes []issueNode) []Issue {
	issues := make([]Issue, 0, len(nodes))
	for _, n := range nodes {
		issues = append(issues, n.toIssue())
	}
	return issues
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("Linear rejected the token — run `treeline auth` to reconnect")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Linear API returned HTTP %d", resp.StatusCode)
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("parsing Linear response: %w", err)
	}
	if len(env.Errors) > 0 {
		return errors.New("Linear: " + env.Errors[0].Message)
	}
	return json.Unmarshal(env.Data, out)
}

const assignedIssuesQuery = `query {
  viewer {
    name email
    assignedIssues(
      filter: { state: { type: { in: ["triage", "backlog", "unstarted", "started"] } } }
      orderBy: updatedAt
      first: 50
    ) {
      nodes {
        id identifier title description priority url
        state { name type }
        assignee { name displayName }
        labels { nodes { name } }
      }
    }
  }
}`

// Viewer identifies the authorized Linear user.
type Viewer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// AssignedIssues returns the viewer and their open assigned issues, most
// recently updated first.
func (c *Client) AssignedIssues(ctx context.Context) (Viewer, []Issue, error) {
	var out struct {
		Viewer struct {
			Viewer
			AssignedIssues struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	}
	if err := c.do(ctx, assignedIssuesQuery, nil, &out); err != nil {
		return Viewer{}, nil, err
	}
	return out.Viewer.Viewer, toIssues(out.Viewer.AssignedIssues.Nodes), nil
}

// SearchQuery is a parsed issue search: free text plus optional narrowing
// by workflow state and assignee, which full-text search cannot see.
type SearchQuery struct {
	Term     string
	State    string
	Assignee string
}

// ParseSearchQuery splits input like "state:review @sam api timeout" into
// tokens: state:<name> narrows by workflow state, @<name> (or
// assignee:<name>) by assignee display name; everything else is the term.
func ParseSearchQuery(raw string) SearchQuery {
	var q SearchQuery
	var terms []string
	for _, f := range strings.Fields(raw) {
		lf := strings.ToLower(f)
		switch {
		case strings.HasPrefix(lf, "state:"):
			q.State = f[len("state:"):]
		case strings.HasPrefix(lf, "assignee:"):
			q.Assignee = f[len("assignee:"):]
		case len(f) > 1 && strings.HasPrefix(f, "@"):
			q.Assignee = f[1:]
		default:
			terms = append(terms, f)
		}
	}
	q.Term = strings.Join(terms, " ")
	return q
}

const issueSelection = `
      id identifier title description priority url
      state { name type }
      assignee { name displayName }
      labels { nodes { name } }`

const searchIssuesQuery = `query Search($term: String!) {
  searchIssues(term: $term, first: 30) {
    nodes {` + issueSelection + `
    }
  }
}`

const filterIssuesQuery = `query Filter($filter: IssueFilter) {
  issues(filter: $filter, orderBy: updatedAt, first: 30) {
    nodes {` + issueSelection + `
    }
  }
}`

// SearchIssues finds issues workspace-wide, whoever they're assigned to.
// A plain term uses Linear's full-text search; state/assignee tokens switch
// to a filtered listing (with the term matched against titles).
func (c *Client) SearchIssues(ctx context.Context, q SearchQuery) ([]Issue, error) {
	if q.State == "" && q.Assignee == "" {
		if q.Term == "" {
			return nil, nil
		}
		var out struct {
			SearchIssues struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"searchIssues"`
		}
		if err := c.do(ctx, searchIssuesQuery, map[string]any{"term": q.Term}, &out); err != nil {
			return nil, err
		}
		return toIssues(out.SearchIssues.Nodes), nil
	}

	filter := map[string]any{}
	if q.State != "" {
		filter["state"] = map[string]any{"name": map[string]any{"containsIgnoreCase": q.State}}
	}
	if q.Assignee != "" {
		filter["assignee"] = map[string]any{"name": map[string]any{"containsIgnoreCase": q.Assignee}}
	}
	if q.Term != "" {
		filter["title"] = map[string]any{"containsIgnoreCase": q.Term}
	}
	var out struct {
		Issues struct {
			Nodes []issueNode `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.do(ctx, filterIssuesQuery, map[string]any{"filter": filter}, &out); err != nil {
		return nil, err
	}
	return toIssues(out.Issues.Nodes), nil
}

const issueQuery = `query Issue($id: String!) {
  issue(id: $id) {
    id identifier title description priority url
    state { name type }
    assignee { name displayName }
    labels { nodes { name } }
  }
}`

// Issue fetches a single issue by identifier (e.g. "LMAP-142").
func (c *Client) Issue(ctx context.Context, key string) (*Issue, error) {
	var out struct {
		Issue *issueNode `json:"issue"`
	}
	if err := c.do(ctx, issueQuery, map[string]any{"id": key}, &out); err != nil {
		return nil, err
	}
	if out.Issue == nil {
		return nil, fmt.Errorf("issue %s not found", key)
	}
	is := out.Issue.toIssue()
	return &is, nil
}

func (c *Client) ViewerName(ctx context.Context) (string, error) {
	var out struct {
		Viewer struct {
			Name string `json:"name"`
		} `json:"viewer"`
	}
	if err := c.do(ctx, `query { viewer { name } }`, nil, &out); err != nil {
		return "", err
	}
	return out.Viewer.Name, nil
}

// PriorityName maps Linear's numeric priority to a label ("" for none).
func PriorityName(p int) string {
	switch p {
	case 1:
		return "urgent"
	case 2:
		return "high"
	case 3:
		return "medium"
	case 4:
		return "low"
	}
	return ""
}
