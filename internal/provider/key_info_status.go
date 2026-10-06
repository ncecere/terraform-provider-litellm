package provider

import (
	"errors"
	"net/http"
)

// LiteLLM 1.104.0 answers /key/info for a deleted or regenerated key with HTTP
// 200 and the archived row (info.status == "deleted", plus deleted_at and
// deleted_by). Only a hash that never existed still returns 404. Every
// /key/info consumer must therefore treat the archived row as absence rather
// than as a live key.

// errKeyInfoArchived reports that /key/info returned LiteLLM's archived row
// for a deleted key. It is typed absence, equivalent to an exact HTTP 404.
var errKeyInfoArchived = errors.New("LiteLLM reported the key as deleted")

// errKeyInfoStatusInvalid reports a /key/info status the provider cannot
// interpret safely. It is never treated as absence.
var errKeyInfoStatusInvalid = errors.New("LiteLLM returned an unrecognized key status")

// keyInfoLiveStatuses are the statuses LiteLLM derives for an existing key:
// "revoked" is a blocked key and "expired" is past its expiry; both rows exist.
var keyInfoLiveStatuses = map[string]struct{}{"active": {}, "expired": {}, "revoked": {}}

// classifyKeyInfoStatus checks the "info" object of a /key/info response.
// It returns nil for a live key, errKeyInfoArchived for a deleted key, and
// errKeyInfoStatusInvalid for anything else.
//
// LiteLLM 1.98.0 returned no status; a status-less row without deletion
// markers is accepted as live so older servers keep working. Only an explicit
// status "deleted" proves absence: LiteLLM 1.104.0 always derives a status for
// its archived rows, so a status-less row with deletion markers is not a shape
// any release returns and is rejected rather than treated as absence.
func classifyKeyInfoStatus(info map[string]interface{}) error {
	deletionMarked := false
	for _, field := range []string{"deleted_at", "deleted_by"} {
		if value, present := info[field]; present && value != nil {
			deletionMarked = true
		}
	}
	raw, present := info["status"]
	if !present || raw == nil {
		if deletionMarked {
			return errKeyInfoStatusInvalid
		}
		return nil
	}
	status, ok := raw.(string)
	if !ok {
		return errKeyInfoStatusInvalid
	}
	if status == "deleted" {
		return errKeyInfoArchived
	}
	if _, live := keyInfoLiveStatuses[status]; live && !deletionMarked {
		return nil
	}
	return errKeyInfoStatusInvalid
}

// isKeyInfoAbsence reports authoritative key absence: an exact HTTP 404 or
// LiteLLM's archived deleted-key row.
func isKeyInfoAbsence(err error) bool {
	return IsAPIErrorStatus(err, http.StatusNotFound) || errors.Is(err, errKeyInfoArchived)
}
