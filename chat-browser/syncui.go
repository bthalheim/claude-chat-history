package main

import (
	"errors"
	"fmt"
)

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (m *model) refreshSync() {
	m.sync = map[string]syncState{}
	for _, it := range m.items {
		if it.isMD {
			continue
		}
		st, err := compareFiles(it.path, m.st.localPath(it.path, it.id))
		if err != nil {
			st = stUnknown
		}
		m.sync[it.id] = st
	}
}

// restore syncs one session into the local store. If that would discard
// local content it asks for confirmation instead.
func (m *model) restore(it item, force bool) {
	if it.isMD {
		m.msg = "only chat sessions can be restored, not daily logs"
		return
	}
	res, err := restoreFile(it.path, m.st.localPath(it.path, it.id), force)
	var nf *needsForceError
	switch {
	case errors.As(err, &nf):
		m.confirm = &it
		m.msg = fmt.Sprintf("Local %s %s. Overwrite (backup kept)? y/n", shortID(it.id), nf.state.describe())
	case err != nil:
		m.msg = "restore failed: " + err.Error()
	default:
		m.msg = shortID(it.id) + ": " + res
	}
	m.refreshSync()
}

// restoreAllSafe restores every session that is missing or out of date
// locally, never touching sessions where local content would be lost.
func (m *model) restoreAllSafe() {
	var done, skipped, failed int
	for _, it := range m.items {
		if it.isMD {
			continue
		}
		switch st := m.sync[it.id]; {
		case st.safe():
			if _, err := restoreFile(it.path, m.st.localPath(it.path, it.id), false); err != nil {
				failed++
			} else {
				done++
			}
		case st == stLocalNewer || st == stDiverged:
			skipped++
		}
	}
	m.msg = fmt.Sprintf("restored/updated %d, skipped %d needing confirmation, %d failed", done, skipped, failed)
	m.refreshSync()
}

// handleConfirm resolves a pending overwrite prompt.
func (m *model) handleConfirm(key string) {
	it := *m.confirm
	m.confirm = nil
	if key == "y" || key == "Y" {
		m.restore(it, true)
		return
	}
	m.msg = "cancelled"
}
