package core

import "testing"

func TestQueryConfigParsesDockerFlag(t *testing.T) {
	_, dcfg, _, exit := queryConfig([]string{"--docker"})
	if exit >= 0 {
		t.Fatalf("exit = %d", exit)
	}
	if !dcfg.docker {
		t.Fatal("queryConfig should enable Docker discovery from --docker")
	}
}

func TestQueryConfig_rejectsBadInput(t *testing.T) {
	for argv, want := range map[string]int{"--ports=abc": 2, "--nope": 2, "-h": 0} {
		if _, _, _, exit := queryConfig([]string{argv}); exit != want {
			t.Errorf("queryConfig(%s) exit = %d, want %d", argv, exit, want)
		}
	}
	if code := cmdReset([]string{"-h"}); code != 0 {
		t.Errorf("reset -h = %d, want 0 (and no reset)", code)
	}
	if code := cmdReset([]string{"--nope"}); code != 2 {
		t.Errorf("reset --nope = %d, want 2", code)
	}
	if code := cmdList([]string{"--ports=1-x"}); code != 2 {
		t.Errorf("list with a bad --ports = %d, want 2", code)
	}
}
