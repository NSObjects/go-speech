package aliyun

import "testing"

func TestNewUsesDefaultEndpoint(t *testing.T) {
	client, err := New(Config{APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if client.cfg.Endpoint != DefaultEndpoint {
		t.Fatalf("endpoint = %q, want %q", client.cfg.Endpoint, DefaultEndpoint)
	}
}

func TestNewPreservesCustomEndpoint(t *testing.T) {
	const endpoint = "wss://workspace.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference"
	client, err := New(Config{APIKey: "test", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if client.cfg.Endpoint != endpoint {
		t.Fatalf("endpoint = %q, want %q", client.cfg.Endpoint, endpoint)
	}
}
