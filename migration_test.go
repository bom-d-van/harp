package main

import (
	"reflect"
	"testing"
)

func TestRetrieveMigrations(t *testing.T) {
	got := retrieveMigrations([]string{
		"AppEnv=prod test/migration.go -arg1 val1 -arg2 val2",
		"AppEnv='pr od' AppEnv='pr od' test/migration.go -arg1 val1 -arg2 val2",
		"AppEnv='pr od' test/migration.go",
	})

	expect := []Migration{
		{
			File: "test/migration.go",
			Envs: "AppEnv=prod",
			Args: "-arg1 val1 -arg2 val2",
		},
		{
			File: "test/migration.go",
			Envs: "AppEnv='pr od' AppEnv='pr od'",
			Args: "-arg1 val1 -arg2 val2",
		},
		{
			File: "test/migration.go",
			Envs: "AppEnv='pr od'",
			Args: "",
		},
	}

	if !reflect.DeepEqual(expect, got) {
		t.Errorf("expect %s got %s", expect, got)
	}
}
