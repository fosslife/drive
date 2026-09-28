package config

import (
	"os"
	"regexp"
	"testing"
)

// composeOnly is read by compose.yml and never by the binary, so it appears in
// .env.example with no constant behind it.
var composeOnly = map[string]bool{"DRIVE_PUBLISH": true}

var (
	constNames   = regexp.MustCompile(`Env\w+\s*=\s*"(DRIVE_\w+)"`)
	documented   = regexp.MustCompile(`(?m)^#?(DRIVE_\w+)=(.*)$`)
	exampleFile  = "../../.env.example"
	configSource = "config.go"
)

// TestEnvExampleIsComplete keeps .env.example honest without anyone having to
// remember to. Adding a variable and not documenting it fails here, as does
// documenting one that no longer exists, and so does a default drifting away
// from the value the file claims it has.
func TestEnvExampleIsComplete(t *testing.T) {
	src, err := os.ReadFile(configSource)
	if err != nil {
		t.Fatal(err)
	}
	example, err := os.ReadFile(exampleFile)
	if err != nil {
		t.Fatal(err)
	}

	real := map[string]bool{}
	for _, m := range constNames.FindAllStringSubmatch(string(src), -1) {
		real[m[1]] = true
	}
	shown := map[string]string{}
	for _, m := range documented.FindAllStringSubmatch(string(example), -1) {
		shown[m[1]] = m[2]
	}

	for name := range real {
		if _, ok := shown[name]; !ok {
			t.Errorf("%s exists but %s does not document it", name, exampleFile)
		}
	}
	for name := range shown {
		if !real[name] && !composeOnly[name] {
			t.Errorf("%s documents %s, which no longer exists", exampleFile, name)
		}
	}

	// Every documented value is the default, so uncommenting a line as printed
	// changes nothing — except for the few that have no default at all, where
	// the value shown is an illustration of the shape rather than a claim.
	illustrative := map[string]bool{
		EnvDataDir:   true, // the image's volume, set by the Dockerfile
		EnvHostname:  true, // unset means plaintext; a name is the operator's
		EnvACMEEmail: true,
	}
	emptyEnv(t)
	t.Setenv(EnvUploadTTL, "")
	want, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range shown {
		if composeOnly[name] || illustrative[name] {
			continue
		}
		t.Setenv(name, value)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("the values in %s do not load: %v", exampleFile, err)
	}
	if got != want {
		t.Errorf("%s documents defaults that have drifted:\n got %+v\nwant %+v", exampleFile, got, want)
	}
}
