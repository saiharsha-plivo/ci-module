package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/ci-module/internal/dagger"

	"gopkg.in/yaml.v3"
)

// GlobalConfig maps the existing globalconfig.yaml structure exactly.
type GlobalConfig struct {
	Defaults         Defaults          `yaml:"defaults"`
	AccountIDs       map[string]string `yaml:"accountIDs"`
	ParentTeamEmails map[string]string `yaml:"parentTeamEmails"`
	ECRRegistries    map[string]string `yaml:"ecrRegistries"`
	ECRLifecycle     map[string]string `yaml:"ecrLifecycle"`
	ECRPolicy        map[string]string `yaml:"ecrPolicy"`
	ArtifactsBuckets string            `yaml:"artifactsBuckets"`
	ECRRegistryURL   map[string]string `yaml:"ecrRegistryUrl"`
	Credentials      Credentials       `yaml:"credentials"`
}

type Defaults struct {
	CheckoutDir              string          `yaml:"checkoutDir"`
	BuildContainer           string          `yaml:"buildContainer"`
	ECRRegistry              string          `yaml:"ecrRegistry"`
	SlackChannel             string          `yaml:"slackChannel"`
	ReleaseSlackRoom         string          `yaml:"releaseslackRoom"`
	AgentLabel               string          `yaml:"agentLabel"`
	BuildNumToKeep           int             `yaml:"buildNumToKeep"`
	ArtifactNumToKeep        int             `yaml:"artifactNumToKeep"`
	TimeoutHrs               int             `yaml:"timeoutHrs"`
	SonarContainer           string          `yaml:"sonarContainer"`
	Semgrep                  string          `yaml:"semgrep"`
	SemgrepScan              bool            `yaml:"semgrepScan"`
	TrivyContainer           string          `yaml:"trivyContainer"`
	TrivyDefaultTeamEmails   string          `yaml:"trivyDefaultTeamEmails"`
	TrivyScanEmailRecipients string          `yaml:"trivyScanEmailRecipients"`
	KubeconformContainer     string          `yaml:"kubeconformContainer"`
	DockerfileLintContainer  string          `yaml:"dockerfileLintContainer"`
	NonAPIParents            string          `yaml:"nonAPIParents"`
	Build                    map[string]LangCommand `yaml:"build"`
	Lint                     LintConfig      `yaml:"lint"`
	UnitTests                UnitTestConfig  `yaml:"unitTests"`
}

type LangCommand struct {
	Command string `yaml:"command"`
}

type LintConfig struct {
	IgnoreFailure bool                  `yaml:"ignoreFailure"`
	Golang        LangLintConfig        `yaml:"golang"`
	Python        LangLintConfig        `yaml:"python"`
	Nodejs        LangLintConfig        `yaml:"nodejs"`
}

type LangLintConfig struct {
	Command    string `yaml:"command"`
	ReportFile string `yaml:"reportFile"`
}

type UnitTestConfig struct {
	IgnoreFailure bool                  `yaml:"ignoreFailure"`
	Golang        GolangTestConfig      `yaml:"golang"`
	Python        LangTestConfig        `yaml:"python"`
	Nodejs        LangTestConfig        `yaml:"nodejs"`
}

type GolangTestConfig struct {
	TestsReportFile    string         `yaml:"testsReportFile"`
	CoverageReportFile string         `yaml:"coverageReportFile"`
	Command            string         `yaml:"command"`
	Msan               ToggleCommand  `yaml:"msan"`
	Datarace           ToggleCommand  `yaml:"datarace"`
	Benchmark          ToggleCommand  `yaml:"benchmark"`
}

type LangTestConfig struct {
	TestsReportFile    string `yaml:"testsReportFile"`
	CoverageReportFile string `yaml:"coverageReportFile"`
	Command            string `yaml:"command"`
}

type ToggleCommand struct {
	Enabled bool   `yaml:"enabled"`
	Command string `yaml:"command"`
}

type Credentials struct {
	ECS    map[string]string `yaml:"ecs"`
	ECR    map[string]string `yaml:"ecr"`
	Lambda map[string]string `yaml:"lambda"`
	Slack  map[string]string `yaml:"slack"`
}

// RepoConfig is the per-repo ci/config.yaml (or ci/config.yml).
type RepoConfig struct {
	Parent         string            `yaml:"parent"`
	ServiceName    string            `yaml:"serviceName"`
	Language       string            `yaml:"language"`
	BuildContainer string            `yaml:"buildContainer"`
	DockerOnly     bool              `yaml:"dockerOnly"`
	Secrets     RepoSecrets       `yaml:"secrets"`
	Lint        *LintOverride     `yaml:"lint,omitempty"`
	UnitTests   *TestOverride     `yaml:"unitTests,omitempty"`
	Deploy      map[string]DeployEnvConfig `yaml:"deploy,omitempty"`
}

type RepoSecrets struct {
	Files map[string]string `yaml:"files"`
}

type LintOverride struct {
	IgnoreFailure *bool  `yaml:"ignoreFailure,omitempty"`
	Command       string `yaml:"command,omitempty"`
}

type TestOverride struct {
	IgnoreFailure *bool  `yaml:"ignoreFailure,omitempty"`
	Command       string `yaml:"command,omitempty"`
	Dependencies  string `yaml:"dependencies,omitempty"`
	Pre           string `yaml:"pre,omitempty"`
}

type DeployEnvConfig struct {
	DeployMethod   string            `yaml:"deployMethod"`
	Regions        []string          `yaml:"regions"`
	SlackChannel   string            `yaml:"slackChannel,omitempty"`
	AutoDeploy     bool              `yaml:"autoDeploy,omitempty"`
	StageAutoDeploy bool             `yaml:"stageAutoDeploy,omitempty"`
	AllowedDeployers string          `yaml:"allowedDeployers,omitempty"`
	AllowedApprovers string          `yaml:"allowedApprovers,omitempty"`
	Extra          map[string]interface{} `yaml:"extra,omitempty"`
}

// RepoParams is the per-repo ci/parameter.yaml — stage toggles.
type RepoParams struct {
	Debug          bool `yaml:"debug"`
	QA             bool `yaml:"qa"`
	SmokeTests     bool `yaml:"smokeTests"`
	Build          bool `yaml:"build"`
	DeployDev      bool `yaml:"deployDev"`
	DeployStaging  bool `yaml:"deployStaging"`
	DeployProd     bool `yaml:"deployProd"`
	PostDeploy     bool `yaml:"postDeploy"`
}

// BuildContext holds the merged configuration used throughout the pipeline.
type BuildContext struct {
	Global      *GlobalConfig
	Repo        *RepoConfig
	Params      *RepoParams
	Version     string
	Branch      string
	CommitSHA   string
	ServiceName string
	Parent      string
	Language    string
}

// LoadGlobalConfig reads a Dagger File and parses globalconfig.yaml.
func LoadGlobalConfig(ctx context.Context, f *dagger.File) (*GlobalConfig, error) {
	data, err := f.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read global config: %w", err)
	}
	var cfg GlobalConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse global config: %w", err)
	}
	return &cfg, nil
}

// LoadRepoConfig reads a Dagger File and parses the per-repo ci/config.yaml.
func LoadRepoConfig(ctx context.Context, f *dagger.File) (*RepoConfig, error) {
	data, err := f.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read repo config: %w", err)
	}
	var cfg RepoConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse repo config: %w", err)
	}
	return &cfg, nil
}

// LoadRepoParams reads a Dagger File and parses the per-repo ci/parameter.yaml.
func LoadRepoParams(ctx context.Context, f *dagger.File) (*RepoParams, error) {
	data, err := f.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read repo params: %w", err)
	}
	var params RepoParams
	if err := yaml.Unmarshal([]byte(data), &params); err != nil {
		return nil, fmt.Errorf("failed to parse repo params: %w", err)
	}
	return &params, nil
}

// NewBuildContext merges global config, repo config, and repo params into a
// single build context — mirroring Jenkins' buildContext << merge behavior.
func NewBuildContext(global *GlobalConfig, repo *RepoConfig, params *RepoParams, branch, commitSHA string) *BuildContext {
	ctx := &BuildContext{
		Global:    global,
		Repo:      repo,
		Params:    params,
		Branch:    branch,
		CommitSHA: commitSHA,
	}

	// Resolve parent: repo config wins, fallback to "plivo"
	ctx.Parent = repo.Parent
	if ctx.Parent == "" {
		ctx.Parent = "plivo"
	}

	// Resolve service name: repo config wins, fallback derived from repo URL
	ctx.ServiceName = repo.ServiceName

	// Resolve language
	ctx.Language = repo.Language

	// Generate version: YY.MM.DD.buildnum format (matches Jenkins)
	// Actual version is set at runtime by the CI step
	ctx.Version = ""

	return ctx
}

// ResolveECRURLs returns the ECR registry URLs for a given parent.
// parent=devops → ecrRegistries[devops]="plivo-devops,plivo-stage"
// → ecrRegistryUrl[plivo-devops], ecrRegistryUrl[plivo-stage]
func (bc *BuildContext) ResolveECRURLs() ([]string, error) {
	registryNames, ok := bc.Global.ECRRegistries[bc.Parent]
	if !ok {
		return nil, fmt.Errorf("no ECR registry mapping for parent %q", bc.Parent)
	}

	var urls []string
	for _, name := range strings.Split(registryNames, ",") {
		name = strings.TrimSpace(name)
		url, ok := bc.Global.ECRRegistryURL[name]
		if !ok {
			return nil, fmt.Errorf("no ECR URL for registry %q", name)
		}
		// Some registries have multiple URLs (e.g. plivo-prod has us-west-1 and us-east-1)
		for _, u := range strings.Split(url, ",") {
			urls = append(urls, strings.TrimSpace(u))
		}
	}
	return urls, nil
}

// ResolveIAMRole returns the IRSA role ARN for a given ECR registry name.
func (bc *BuildContext) ResolveIAMRole(registryName string) string {
	accountID, ok := bc.Global.AccountIDs[registryName]
	if !ok {
		return ""
	}
	// Reverse lookup: accountID is the value in the original config
	// but in our struct it's key=accountID, value=name
	// The globalconfig has it as accountID → name, so we need the ID
	return fmt.Sprintf("arn:aws:iam::%s:role/woodpecker-ci", accountID)
}

// LintCommand returns the lint command for the configured language.
func (bc *BuildContext) LintCommand() string {
	if bc.Repo.Lint != nil && bc.Repo.Lint.Command != "" {
		return bc.Repo.Lint.Command
	}
	switch bc.Language {
	case "golang":
		return bc.Global.Defaults.Lint.Golang.Command
	case "python":
		return bc.Global.Defaults.Lint.Python.Command
	case "nodejs":
		return bc.Global.Defaults.Lint.Nodejs.Command
	default:
		return ""
	}
}

// TestCommand returns the unit test command for the configured language.
func (bc *BuildContext) TestCommand() string {
	if bc.Repo.UnitTests != nil && bc.Repo.UnitTests.Command != "" {
		return bc.Repo.UnitTests.Command
	}
	switch bc.Language {
	case "golang":
		return bc.Global.Defaults.UnitTests.Golang.Command
	case "python":
		return bc.Global.Defaults.UnitTests.Python.Command
	case "nodejs":
		return bc.Global.Defaults.UnitTests.Nodejs.Command
	default:
		return ""
	}
}

// LintIgnoreFailure returns whether lint failures should be ignored.
func (bc *BuildContext) LintIgnoreFailure() bool {
	if bc.Repo.Lint != nil && bc.Repo.Lint.IgnoreFailure != nil {
		return *bc.Repo.Lint.IgnoreFailure
	}
	return bc.Global.Defaults.Lint.IgnoreFailure
}

// TestIgnoreFailure returns whether test failures should be ignored.
func (bc *BuildContext) TestIgnoreFailure() bool {
	if bc.Repo.UnitTests != nil && bc.Repo.UnitTests.IgnoreFailure != nil {
		return *bc.Repo.UnitTests.IgnoreFailure
	}
	return bc.Global.Defaults.UnitTests.IgnoreFailure
}

// SlackChannel returns the notification channel for a given deploy environment.
func (bc *BuildContext) SlackChannel(deployEnv string) string {
	if deploy, ok := bc.Repo.Deploy[deployEnv]; ok && deploy.SlackChannel != "" {
		return deploy.SlackChannel
	}
	if deployEnv == "prod" {
		return bc.Global.Defaults.ReleaseSlackRoom
	}
	return bc.Global.Defaults.SlackChannel
}

// TeamEmails returns the team emails for the configured parent.
func (bc *BuildContext) TeamEmails() string {
	if emails, ok := bc.Global.ParentTeamEmails[bc.Parent]; ok {
		return emails
	}
	return bc.Global.ParentTeamEmails["devops"]
}
