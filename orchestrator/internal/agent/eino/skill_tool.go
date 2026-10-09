package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// skill_tool.go is the Eino counterpart of the graph engine's [SKILL_VIEW]
// action (node_skill.go). The SKILLS index in the instruction names every
// playbook; this native tool returns one playbook's full text on demand. Both
// engines render the same skill.RenderBody and record the same ActSkill
// activity, so the TRACE view reads the same whichever engine ran the task.

type skillViewDeps struct {
	taskID    string
	sessionID string
	store     *agent.Store
	skills    *skill.Registry
}

type skillViewArgs struct {
	Name string `json:"name"`
}

var skillViewInfo = &schema.ToolInfo{
	Name: "skill_view",
	Desc: "Load the full playbook named in the SKILLS index (its method and steps) into your context. " +
		"Call it before a technique a playbook covers, then follow that playbook's method rather than improvising one.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"name": {Type: "string", Desc: "Exact playbook name from the SKILLS index", Required: true},
	}),
}

type skillViewTool struct{ deps skillViewDeps }

func newSkillViewTool(deps skillViewDeps) *skillViewTool { return &skillViewTool{deps: deps} }

func (st *skillViewTool) Info(_ context.Context) (*schema.ToolInfo, error) { return skillViewInfo, nil }

func (st *skillViewTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args skillViewArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("invalid tool call arguments: %v", err), nil
	}
	name := strings.TrimSpace(args.Name)
	sk, ok := st.deps.skills.Lookup(name)
	if !ok {
		return fmt.Sprintf("no playbook named %q — use an exact name from the SKILLS index", name), nil
	}
	if st.deps.store != nil {
		st.deps.store.RecordActivity(agent.ActivityEvent{
			SessionID: st.deps.sessionID, TaskID: st.deps.taskID, Kind: agent.ActSkill, Summary: sk.Name,
		})
	}
	return skill.RenderBody(sk), nil
}
