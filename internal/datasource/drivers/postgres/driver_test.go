// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"context"
	"testing"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func TestNewDriverRequiresConnection(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{Name: "pg", Type: Type, Raw: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when connection is missing")
	}
}

func TestNewDriverRejectsBadURL(t *testing.T) {
	cases := map[string]map[string]any{
		"wrong scheme":  {"connection": "mysql://u@h/db"},
		"missing user":  {"connection": "postgres://h:5432/db"},
		"missing host":  {"connection": "postgres://u@/db"},
		"missing db":    {"connection": "postgres://u@h:5432/"},
		"bad port":      {"connection": "postgres://u@h:99999/db"},
		"invalid parse": {"connection": "::not-a-url::"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newDriver(context.Background(), pkgds.Spec{Name: "pg", Type: Type, Raw: raw})
			if err == nil {
				t.Errorf("expected error for %q: got nil", name)
			}
		})
	}
}

func TestNewDriverParsesURL(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "pg", Type: Type,
		Raw: map[string]any{
			"connection":     "postgres://svc@db.example:6543/orders?sslmode=require",
			"credentialsRef": "secret://env/PG_PASSWORD",
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if d.host != "db.example" || d.port != 6543 || d.database != "orders" || d.user != "svc" {
		t.Errorf("parsed parts wrong: %+v", d)
	}
	if d.sslmode != "require" {
		t.Errorf("sslmode = %q; want require", d.sslmode)
	}
	if d.credentialsRef != "secret://env/PG_PASSWORD" {
		t.Errorf("credentialsRef not captured")
	}
	if !d.Readonly() {
		t.Error("postgres driver must be read-only in MVP")
	}
	if err := ds.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNewDriverDefaultsPortAndSSLMode(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "pg", Type: Type,
		Raw: map[string]any{"connection": "postgres://svc@db/orders"},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if d.port != 5432 {
		t.Errorf("port default = %d; want 5432", d.port)
	}
	if d.sslmode != "prefer" {
		t.Errorf("sslmode default = %q; want prefer", d.sslmode)
	}
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("postgres driver did not self-register via init()")
	}
}

type fakeResolver struct{ result []byte }

func (f fakeResolver) Resolve(_ context.Context, _ string) ([]byte, error) { return f.result, nil }

func TestResolvePasswordReturnsEmptyWhenUnset(t *testing.T) {
	d := &driver{}
	pw, err := d.resolvePassword(context.Background(), pkgds.AttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pw != "" {
		t.Errorf("want empty password; got %q", pw)
	}
}

func TestResolvePasswordRequiresResolver(t *testing.T) {
	d := &driver{credentialsRef: "secret://env/x"}
	_, err := d.resolvePassword(context.Background(), pkgds.AttachOptions{})
	if err == nil {
		t.Fatal("expected error when credentialsRef is set but resolver is nil")
	}
}

func TestResolvePasswordTrimsNewlines(t *testing.T) {
	d := &driver{credentialsRef: "secret://env/x"}
	pw, err := d.resolvePassword(context.Background(), pkgds.AttachOptions{
		SecretResolver: fakeResolver{result: []byte("hunter2\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pw != "hunter2" {
		t.Errorf("pw = %q; want hunter2", pw)
	}
}
