package volcengine

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestSessionFinishedStatusValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		code    speecherr.Code
		remote  string
	}{
		{"no body", "", "", ""},
		{"usage only", `{"usage":{"characters":2}}`, "", ""},
		{"success", `{"status_code":20000000}`, "", ""},
		{"null status", `{"status_code":null}`, "", ""},
		{"string status", `{"status_code":"20000000"}`, speecherr.CodeProtocol, ""},
		{"fractional status", `{"status_code":20000000.5}`, speecherr.CodeProtocol, ""},
		{"null metadata", "null", speecherr.CodeProtocol, ""},
		{"invalid metadata", "{bad json", speecherr.CodeProtocol, ""},
		{"remote failure", `{"status_code":45000000,"message":"denied"}`, speecherr.CodeUnauthorized, "45000000"},
		{"negative status", `{"status_code":-1}`, speecherr.CodeProtocol, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (wireFrame{payload: []byte(tc.payload)}).sessionStatus()
			if tc.code == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !speecherr.IsCode(err, tc.code) {
				t.Fatalf("error = %v; want code %s", err, tc.code)
			}
			if tc.remote != "" {
				var remote *speecherr.Error
				if !errors.As(err, &remote) || remote.RemoteCode != tc.remote {
					t.Fatalf("remote code not retained: %v", err)
				}
			}
		})
	}
}
