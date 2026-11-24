package server

import (
	"fmt"
	"net/http"
	"testing"
)

// A channel can be authorized through its group even when the channel's device
// is not bound to any group. The device-group Pluck must not overwrite the
// channel-group IDs already collected.
func TestRequireGroupPermChannelOnlyBinding(t *testing.T) {
	srv, _, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)
	var roleResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/roles", admin, map[string]any{
		"name": "groupperm-restricted", "permissions": "event",
	}, &roleResp)
	if roleResp.Code != 0 {
		t.Fatalf("create role: %+v", roleResp)
	}
	_, chID := newSnapshotChannel(t, app, "groupperm")
	userID, token := snapshotUser(t, srv, admin, "grouppermuser", "groupperm-restricted")
	groupID := snapshotGroup(t, srv, admin, "groupperm")

	var r apiResp
	code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin, map[string]any{
		"groupId": groupID, "channelIds": []uint{chID},
	}, &r)
	if code != http.StatusOK || r.Code != 0 {
		t.Fatalf("bind channel: %d %+v", code, r)
	}
	code = doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/user-groups", admin, map[string]any{
		"userId": userID, "groupId": groupID, "permissions": "video",
	}, &r)
	if code != http.StatusOK || r.Code != 0 {
		t.Fatalf("assign user group: %d %+v", code, r)
	}

	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots?channelId=%d", srv.URL, chID), token, nil, &r)
	if code != http.StatusOK || r.Code != 0 {
		t.Fatalf("channel-only group membership should authorize: %d %+v", code, r)
	}
}
