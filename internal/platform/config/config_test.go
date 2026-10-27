package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{name: "default address", env: map[string]string{}, want: ":8080"},
		{name: "explicit address", env: map[string]string{"BIRGE_HTTP_ADDR": "127.0.0.1:9000"}, want: "127.0.0.1:9000"},
		{name: "invalid address", env: map[string]string{"BIRGE_HTTP_ADDR": "nope"}, wantErr: true},
		{name: "invalid crisis phones", env: map[string]string{"BIRGE_CRISIS_PHONES": "[{"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg.HTTPAddr != tt.want {
				t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, tt.want)
			}
		})
	}
}
