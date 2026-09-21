package acp

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// TestRebuildSessionPrefersRebuilderSeam pins that a session config switch
// rebuilds through the SessionRebuilder seam whenever the factory provides it.
// boot.Rebuild then carries the outgoing controller's v3 session binding
// (service + runtime) into the replacement, so the chat keeps writing to the
// same storage session.
//
// Regression: rebuildSessionLocked used to call plain Factory.NewSession,
// dropping that binding. AdoptHistory's empty-path branch then kept the
// replacement in memory, and the next turn minted a brand-new storage session
// — one client conversation was split into one session per config change
// (e.g. every session/set_config_option call from Zed/JetBrains).
func TestRebuildSessionPrefersRebuilderSeam(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess-rebind.jsonl")
	base := agent.NewSession("sys prompt")
	base.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := base.Save(path); err != nil {
		t.Fatalf("save session: %v", err)
	}

	sink := newUpdateSink(&fakeNotifier{}, "sess-rebind")
	sess := &acpSession{
		id:         "sess-rebind",
		sink:       sink,
		cwd:        dir,
		model:      "fast",
		transcript: path,
		modeID:     sessionModeNormal,
	}
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("acquire session lease: %v", err)
	}
	sess.lease = lease
	t.Cleanup(sess.releaseSessionLease)
	t.Cleanup(func() {
		if ctrl := sess.currentCtrl(); ctrl != nil {
			ctrl.Close()
		}
	})

	factory := &reloadFactory{configurableFactory: &configurableFactory{dir: dir}}
	svc := &service{
		factory:  factory,
		sessions: map[string]*acpSession{sess.id: sess},
		clientCaps: ClientCapabilities{
			FS:       FSCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
	}
	oldCtrl := control.New(control.Options{
		Executor:    agent.New(nil, nil, base, agent.Options{}, event.Discard),
		SessionDir:  dir,
		SessionPath: path,
		Label:       "fast",
	})
	sess.ctrl = oldCtrl

	if err := svc.rebuildSession(context.Background(), sess, SessionConfigState{Model: "pro"}, []sessionConfigDelta{{axis: "model", model: "pro"}}); err != nil {
		t.Fatalf("rebuildSession: %v", err)
	}
	if factory.rebuildCalls != 1 {
		t.Fatalf("RebuildSession calls = %d, want 1 (the rebuild must carry the outgoing controller's session binding into the replacement)", factory.rebuildCalls)
	}
	if factory.buildCount() != 0 {
		t.Fatalf("plain NewSession builds = %d, want 0 while the SessionRebuilder seam is available", factory.buildCount())
	}
}
