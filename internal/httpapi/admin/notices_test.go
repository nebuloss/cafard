package admin_test

import "testing"

// A notice is there while its problem is: marked read, it says so; once the
// problem is dealt with it goes, and its mark with it.
func TestNotices(t *testing.T) {
	r := newRig(t)
	r.signIn()
	r.call("POST", "/admin/api/projects", map[string]any{"name": "lonely"})
	find := func() map[string]any {
		_, out := r.call("GET", "/admin/api/notices", nil)
		list, _ := out["notices"].([]any)
		for _, n := range list {
			if n := n.(map[string]any); n["id"] == "empty:lonely" {
				return n
			}
		}
		return nil
	}
	n := find()
	if n == nil || n["link"] != "/projects/lonely" || n["read"] != nil {
		t.Fatalf("an empty project's notice: %v", n)
	}
	if code, _ := r.call("POST", "/admin/api/notices/read", map[string]any{"ids": []string{"empty:lonely"}}); code != 204 {
		t.Fatalf("mark read: %d", code)
	}
	if n := find(); n == nil || n["read"] == nil {
		t.Errorf("after marking it read: %v", n)
	}
	r.call("PATCH", "/admin/api/projects/lonely", map[string]any{"members": []string{"guillaume"}})
	if n := find(); n != nil {
		t.Errorf("the notice outlived its problem: %v", n)
	}
	if marks, _ := r.st.NoticesRead(ctx); len(marks) != 0 {
		t.Errorf("its read mark was kept: %v", marks)
	}
}
