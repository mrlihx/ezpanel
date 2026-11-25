package security

import "context"

// 防火墙列表仅展示面板管理的可编辑规则，不再混入 Docker 自动链或只读内置行。
func (m *Module) firewallRows(_ context.Context, state firewallState, _ []int, _ bool) ([]firewallRow, string) {
	rows := make([]firewallRow, 0, len(state.Rules))
	for _, row := range state.Rules {
		rows = append(rows, firewallRow{Rule: row})
	}
	return rows, ""
}
