package linear

import (
	"encoding/json"
	"testing"
)

// TestAssigneeLabel: Linear leaves name set to the email address for members
// who never set one, so displayName has to stand in.
func TestAssigneeLabel(t *testing.T) {
	for _, tc := range []struct{ name, displayName, want string }{
		{"Travis Low", "travis", "Travis Low"},
		{"Nina Chadakorn", "nina", "Nina Chadakorn"},
		{"sharan", "sharan", "sharan"},
		{"mark.cipolla@labflow.ai", "mark.cipolla", "mark.cipolla"},
		{"tristan@labflow.ai", "tristan", "tristan"},
		// no displayName to fall back on: keep the local part
		{"chayut@labflow.ai", "", "chayut"},
		{"", "brendan", "brendan"},
		{"", "", ""},
	} {
		if got := assigneeLabel(tc.name, tc.displayName); got != tc.want {
			t.Errorf("assigneeLabel(%q, %q) = %q, want %q", tc.name, tc.displayName, got, tc.want)
		}
	}
}

// TestIssueAssigneeDecoding covers the whole path from the GraphQL payload,
// including an unassigned card and a null assignee.
func TestIssueAssigneeDecoding(t *testing.T) {
	const payload = `[
	  {"identifier":"LAB-1","title":"one","assignee":{"name":"Travis Low","displayName":"travis"}},
	  {"identifier":"LAB-2","title":"two","assignee":{"name":"mark.cipolla@labflow.ai","displayName":"mark.cipolla"}},
	  {"identifier":"LAB-3","title":"three","assignee":null},
	  {"identifier":"LAB-4","title":"four"}
	]`
	var nodes []issueNode
	if err := json.Unmarshal([]byte(payload), &nodes); err != nil {
		t.Fatal(err)
	}
	issues := toIssues(nodes)
	want := []string{"Travis Low", "mark.cipolla", "", ""}
	if len(issues) != len(want) {
		t.Fatalf("got %d issues, want %d", len(issues), len(want))
	}
	for i, w := range want {
		if issues[i].Assignee != w {
			t.Errorf("%s assignee = %q, want %q", issues[i].Identifier, issues[i].Assignee, w)
		}
	}
}
