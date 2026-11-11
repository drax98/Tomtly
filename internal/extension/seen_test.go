package extension

import "context"

// These add, approve and replace the way the page does: with the access it was just shown.

func addSeen(m *Manager, ctx context.Context, source string, settings map[string]any) (Status, error) {
	shown, err := m.Inspect(ctx, source)
	if err != nil {
		return m.Add(ctx, source, settings, nil)
	}
	return m.Add(ctx, source, settings, shown.Access)
}

func replaceSeen(m *Manager, ctx context.Context, source, newSource string, given map[string]any) (Status, error) {
	shown, err := m.Inspect(ctx, newSource)
	if err != nil {
		return m.Replace(ctx, source, newSource, given, nil)
	}
	return m.Replace(ctx, source, newSource, given, shown.Access)
}

func approveSeen(m *Manager, source string) (string, error) {
	inst := m.bySource(source)
	if inst == nil {
		return m.Approve(source, nil)
	}
	return m.Approve(source, inst.currentStatus().Access)
}
