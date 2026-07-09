package srun

import "testing"

func TestAuthValuesMatchKnownAuthVector(t *testing.T) {
	config := DefaultConfig()
	username := "user"
	password := "pass"
	ip := "10.0.0.2"
	token := "token123"

	hmd5 := HMACMD5Hex(token, password)
	if hmd5 != "d577c7ef5b341803e5311a44b1b4258d" {
		t.Fatalf("hmd5 mismatch: %s", hmd5)
	}

	info, err := Info(username, password, ip, token, config)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo := "{SRBX1}PRcvPe0z1SUzdwLuXaYzuPCoaiOPthLxJ6UdFPNsYrXs/uabXR/TZFudS3wjs59gxwvkCgIMbdbp3/6XORY5sKxTM1Og0xIMOViUhBNA8DlW/S6mt0lhGSFKCd+="
	if info != wantInfo {
		t.Fatalf("info mismatch:\nwant %s\n got %s", wantInfo, info)
	}

	chksum := Checksum(token, username, hmd5, ip, info, config)
	if chksum != "8ab41acd3eceee0198a04df56b02f9516a04a087" {
		t.Fatalf("chksum mismatch: %s", chksum)
	}
}

func TestParseJSONP(t *testing.T) {
	got, err := ParseJSONP(`jQuery123({"challenge":"abc","error":"ok"});`)
	if err != nil {
		t.Fatal(err)
	}
	if got["challenge"] != "abc" || got["error"] != "ok" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestOK(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{name: "login ok", in: map[string]any{"suc_msg": "login_ok"}, want: true},
		{name: "already online", in: map[string]any{"suc_msg": "ip_already_online_error"}, want: true},
		{name: "error ok", in: map[string]any{"error": "ok"}, want: true},
		{name: "failed", in: map[string]any{"error_msg": "password_error"}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OK(tc.in); got != tc.want {
				t.Fatalf("OK() = %v, want %v", got, tc.want)
			}
		})
	}
}
