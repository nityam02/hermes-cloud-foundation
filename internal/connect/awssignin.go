package connect

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+@-]{0,63}$`)
	awsRegionPattern   = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]{1,2}$`)
	awsVersionPattern  = regexp.MustCompile(`aws-cli/(\d+)\.(\d+)\.(\d+)`)
)

// AWSCLI describes the AWS CLI on this machine. Console sign-in (`aws login`) needs 2.32 or later.
type AWSCLI struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Login     bool   `json:"login"` // supports `aws login`
}

// FindAWSCLI looks for the AWS CLI and reads its version.
func FindAWSCLI(ctx context.Context) AWSCLI {
	path, ok := FindTool("aws")
	if !ok {
		return AWSCLI{}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out []string
	_ = RunTool(ctx, path, []string{"--version"}, func(l string) { out = append(out, l) })
	cli := AWSCLI{Installed: true}
	if m := awsVersionPattern.FindStringSubmatch(strings.Join(out, " ")); m != nil {
		cli.Version = m[1] + "." + m[2] + "." + m[3]
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		cli.Login = major > 2 || (major == 2 && minor >= 32)
	}
	return cli
}

// AWSSignInRequest names the profile to sign in. A name that is not in the AWS config yet becomes a
// new console sign-in profile.
type AWSSignInRequest struct {
	Profile string `json:"profile"`
	Region  string `json:"region"`
}

// AWSSignIn signs the operator in with the AWS CLI: `aws login` (the AWS console sign-in, in the
// browser) or `aws sso login` for IAM Identity Center profiles. Hermes sees no password or key; the
// CLI keeps the short-lived credentials in its own cache.
func AWSSignIn(ctx context.Context, req AWSSignInRequest, p Progress) error {
	if !profileNamePattern.MatchString(req.Profile) {
		return errors.New("a profile name is letters, digits and . _ + @ - (up to 64 characters)")
	}
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	if !awsRegionPattern.MatchString(req.Region) {
		return fmt.Errorf("%q is not an AWS region name like us-east-1", req.Region)
	}
	aws, ok := FindTool("aws")
	if !ok {
		return errors.New("the AWS CLI is not installed on this machine. Install version 2.32 or later (https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html), then try again")
	}
	kind := ""
	for _, pr := range AWSProfiles() {
		if pr.Name == req.Profile {
			kind = pr.Kind
		}
	}
	switch kind {
	case "sso":
		p.Step("signin", "running", "Approve the sign-in in your browser. If the page shows a code, check it matches the one here.")
		if err := signIn(ctx, aws, []string{"sso", "login", "--profile", req.Profile}, true, p); err != nil {
			return err
		}
		p.Step("signin", "done", "Signed in through IAM Identity Center.")
		return nil
	case "keys":
		return errors.New("this profile uses access keys saved on this machine, so there is nothing to sign in to. Pick another profile, or make a new sign-in")
	case "role", "process":
		return errors.New("this profile gets its credentials from another profile or a helper program. Sign in to that one instead")
	case "":
		p.Step("profile", "running", "Adding the profile "+req.Profile+" to your AWS config.")
		if err := RunTool(ctx, aws, []string{"configure", "set", "region", req.Region, "--profile", req.Profile}, p.Line); err != nil {
			return fmt.Errorf("could not add the profile: %w", err)
		}
		p.Step("profile", "done", "Added the profile "+req.Profile+".")
	}
	if cli := FindAWSCLI(ctx); !cli.Login {
		return fmt.Errorf("signing in from Hermes needs AWS CLI 2.32 or later for `aws login`; this machine has %s. Update the AWS CLI, then try again", firstNonEmpty(cli.Version, "an older version"))
	}
	p.Step("signin", "running", "Sign in to the AWS console in the browser tab that opened, then choose this session.")
	if err := signIn(ctx, aws, []string{"login", "--profile", req.Profile, "--region", req.Region}, false, p); err != nil {
		return err
	}
	p.Step("signin", "done", "Signed in.")
	return nil
}
