// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"context"
	"testing"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func TestNewDriverRequiresConnection(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{Name: "my", Type: Type, Raw: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when connection is missing")
	}
}

func TestNewDriverRejectsBadURL(t *testing.T) {
	cases := map[string]map[string]any{
		"wrong scheme":  {"connection": "postgres://u@h:3306/db"},
		"missing user":  {"connection": "mysql://h:3306/db"},
		"missing host":  {"connection": "mysql://u@/db"},
		"missing db":    {"connection": "mysql://u@h:3306/"},
		"bad port":      {"connection": "mysql://u@h:99999/db"},
		"invalid parse": {"connection": "::not-a-url::"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newDriver(context.Background(), pkgds.Spec{Name: "my", Type: Type, Raw: raw})
			if err == nil {
				t.Errorf("expected error for %q", name)
			}
		})
	}
}

func TestNewDriverParsesURL(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "my", Type: Type,
		Raw: map[string]any{
			"connection":     "mysql://svc@db.example:3307/orders?ssl_mode=required",
			"credentialsRef": "secret://env/MYSQL_PASSWORD",
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if d.host != "db.example" || d.port != 3307 || d.database != "orders" || d.user != "svc" {
		t.Errorf("parsed parts wrong: %+v", d)
	}
	if d.sslmode != "required" {
		t.Errorf("sslmode = %q; want required", d.sslmode)
	}
	if d.credentialsRef != "secret://env/MYSQL_PASSWORD" {
		t.Errorf("credentialsRef not captured")
	}
	if !d.Readonly() {
		t.Error("mysql driver must be read-only in MVP")
	}
	if err := ds.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestNewDriverDefaultsPortAndSSL(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "my", Type: Type,
		Raw: map[string]any{"connection": "mariadb://svc@db/orders"},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if d.port != 3306 {
		t.Errorf("port default = %d; want 3306", d.port)
	}
	if d.sslmode != "preferred" {
		t.Errorf("sslmode default = %q; want preferred", d.sslmode)
	}
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("mysql driver did not self-register via init()")
	}
}

type fakeResolver struct{ result []byte }

func (f fakeResolver) Resolve(_ context.Context, _ string) ([]byte, error) { return f.result, nil }

func TestResolvePassword(t *testing.T) {
	d := &driver{}
	pw, err := d.resolvePassword(context.Background(), pkgds.AttachOptions{})
	if err != nil || pw != "" {
		t.Fatalf("want empty/nil; got pw=%q err=%v", pw, err)
	}

	d = &driver{credentialsRef: "secret://env/x"}
	if _, err := d.resolvePassword(context.Background(), pkgds.AttachOptions{}); err == nil {
		t.Fatal("expected error when resolver is nil")
	}

	pw, err = d.resolvePassword(context.Background(), pkgds.AttachOptions{
		SecretResolver: fakeResolver{result: []byte("p@ss\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pw != "p@ss" {
		t.Errorf("pw = %q; want p@ss", pw)
	}
}
