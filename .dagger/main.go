// CI Module — pipeline functions mirroring the Jenkins shared library.
//
// All container images are resolved from globalconfig.yaml.
// Each function is called as an individual Woodpecker step.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"dagger/ci-module/internal/dagger"
)

// CiModule is the main Dagger module entrypoint.
type CiModule struct{}

func logger(step string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("step", step, "ts", time.Now().UTC().Format(time.RFC3339))
}

func loadContext(ctx context.Context, globalConfig *dagger.File, repoConfig *dagger.File, branch, commitSHA string) (*BuildContext, error) {
	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return nil, err
	}
	repo, err := LoadRepoConfig(ctx, repoConfig)
	if err != nil {
		return nil, err
	}
	return NewBuildContext(global, repo, &RepoParams{}, branch, commitSHA), nil
}

func resolveBuildContainer(bc *BuildContext) string {
	if bc.Repo.BuildContainer != "" {
		return bc.Repo.BuildContainer
	}
	return bc.Global.Defaults.BuildContainer
}

// ---------------------------------------------------------------------------
// Debug
// ---------------------------------------------------------------------------

// Debug logs the full merged build context — config, parameters, images, ECR URLs, commands.
// Mirrors Jenkins debug stage. Use to verify config resolution before running the pipeline.
func (m *CiModule) Debug(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	// +optional
	// +default=""
	language string,
	// +optional
	// +default=""
	branch string,
	// +optional
	// +default=""
	commitSHA string,
	// +optional
	repoParams *dagger.File,
) (string, error) {
	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return "", fmt.Errorf("debug: %w", err)
	}
	repo, err := LoadRepoConfig(ctx, repoConfig)
	if err != nil {
		return "", fmt.Errorf("debug: %w", err)
	}

	params := &RepoParams{}
	if repoParams != nil {
		params, err = LoadRepoParams(ctx, repoParams)
		if err != nil {
			return "", fmt.Errorf("debug: %w", err)
		}
	}

	bc := NewBuildContext(global, repo, params, branch, commitSHA)
	if language != "" {
		bc.Language = language
	}

	p := func(label, value string) string { return fmt.Sprintf("%-28s %s\n", label, value) }
	pb := func(label string, value bool) string { return fmt.Sprintf("%-28s %v\n", label, value) }
	section := func(title string) string { return fmt.Sprintf("\n[%s]\n", title) }

	var out strings.Builder

	out.WriteString(section("BUILD CONTEXT"))
	out.WriteString(p("parent:", bc.Parent))
	out.WriteString(p("serviceName:", bc.ServiceName))
	out.WriteString(p("language:", bc.Language))
	out.WriteString(p("branch:", bc.Branch))
	out.WriteString(p("commitSHA:", bc.CommitSHA))
	out.WriteString(pb("dockerOnly:", bc.Repo.DockerOnly))

	out.WriteString(section("PARAMETERS"))
	out.WriteString(pb("debug:", params.Debug))
	out.WriteString(pb("qa:", params.QA))
	out.WriteString(pb("smokeTests:", params.SmokeTests))
	out.WriteString(pb("build:", params.Build))
	out.WriteString(pb("deployDev:", params.DeployDev))
	out.WriteString(pb("deployStaging:", params.DeployStaging))
	out.WriteString(pb("deployProd:", params.DeployProd))
	out.WriteString(pb("postDeploy:", params.PostDeploy))

	out.WriteString(section("IMAGES"))
	out.WriteString(p("buildContainer:", resolveBuildContainer(bc)))
	out.WriteString(p("trivyContainer:", bc.Global.Defaults.TrivyContainer))
	out.WriteString(p("semgrep:", bc.Global.Defaults.Semgrep))
	out.WriteString(p("dockerfileLint:", bc.Global.Defaults.DockerfileLintContainer))
	out.WriteString(p("kubeconform:", bc.Global.Defaults.KubeconformContainer))

	out.WriteString(section("COMMANDS"))
	out.WriteString(p("lint:", bc.LintCommand()))
	out.WriteString(p("test:", bc.TestCommand()))
	out.WriteString(pb("lint ignoreFailure:", bc.LintIgnoreFailure()))
	out.WriteString(pb("test ignoreFailure:", bc.TestIgnoreFailure()))

	out.WriteString(section("ECR"))
	ecrURLs, ecrErr := bc.ResolveECRURLs()
	if ecrErr != nil {
		out.WriteString(p("error:", ecrErr.Error()))
	} else {
		for i, url := range ecrURLs {
			out.WriteString(p(fmt.Sprintf("registry[%d]:", i), url))
		}
	}

	out.WriteString(section("TAGS"))
	tags := generateTags(branch, commitSHA, "")
	for _, tag := range tags {
		out.WriteString(fmt.Sprintf("  %s\n", tag))
	}

	out.WriteString(section("NOTIFICATIONS"))
	out.WriteString(p("slackChannel:", bc.SlackChannel("")))
	out.WriteString(p("slackChannel (prod):", bc.SlackChannel("prod")))
	out.WriteString(p("teamEmails:", bc.TeamEmails()))

	if bc.Repo.Lint != nil && bc.Repo.Lint.Command != "" {
		out.WriteString(section("REPO OVERRIDES"))
		out.WriteString(p("lint command:", bc.Repo.Lint.Command))
	}
	if bc.Repo.UnitTests != nil && bc.Repo.UnitTests.Command != "" {
		out.WriteString(p("test command:", bc.Repo.UnitTests.Command))
	}

	if len(bc.Repo.Deploy) > 0 {
		out.WriteString(section("DEPLOY CONFIG"))
		for env, cfg := range bc.Repo.Deploy {
			out.WriteString(fmt.Sprintf("  %s:\n", env))
			out.WriteString(p("    method:", cfg.DeployMethod))
			out.WriteString(p("    regions:", fmt.Sprintf("%v", cfg.Regions)))
		}
	}

	if len(bc.Repo.Secrets.Files) > 0 {
		out.WriteString(section("SECRETS"))
		for k, v := range bc.Repo.Secrets.Files {
			out.WriteString(p("  "+k+":", v))
		}
	}

	out.WriteString(section("SOURCE FILES"))
	entries, _ := src.Entries(ctx)
	for _, entry := range entries {
		out.WriteString(fmt.Sprintf("  %s\n", entry))
	}

	return out.String(), nil
}

// ---------------------------------------------------------------------------
// Lint
// ---------------------------------------------------------------------------

// Lint runs language-specific linting inside the buildContainer from globalconfig.
func (m *CiModule) Lint(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	language string,
) (string, error) {
	log := logger("lint")
	log.Info("starting lint", "language", language)

	bc, err := loadContext(ctx, globalConfig, repoConfig, "", "")
	if err != nil {
		return "", fmt.Errorf("lint: %w", err)
	}
	bc.Language = language

	cmd := bc.LintCommand()
	if cmd == "" {
		log.Info("no lint command configured, skipping", "language", language)
		return "skipped: no lint command for " + language, nil
	}

	image := resolveBuildContainer(bc)
	log.Info("running lint", "image", image, "command", cmd)

	out, err := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"sh", "-c", cmd}).
		Stdout(ctx)

	if err != nil {
		if bc.LintIgnoreFailure() {
			log.Warn("lint failed but ignoreFailure=true", "error", err)
			return "lint failed (ignored): " + err.Error(), nil
		}
		log.Error("lint failed", "error", err)
		return "", fmt.Errorf("lint failed: %w", err)
	}

	log.Info("lint passed")
	return out, nil
}

// ---------------------------------------------------------------------------
// DockerfileLint
// ---------------------------------------------------------------------------

// DockerfileLint runs dockerfile linting using dockerfileLintContainer from globalconfig.
func (m *CiModule) DockerfileLint(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	// +optional
	// +default="Dockerfile"
	dockerfile string,
) (string, error) {
	log := logger("dockerfile-lint")
	log.Info("linting Dockerfile", "file", dockerfile)

	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return "", fmt.Errorf("dockerfile-lint: %w", err)
	}

	image := global.Defaults.DockerfileLintContainer
	log.Info("using image", "image", image)

	out, err := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"hadolint", dockerfile}).
		Stdout(ctx)

	if err != nil {
		log.Warn("dockerfile lint found issues", "error", err)
		return "", fmt.Errorf("dockerfile lint failed: %w", err)
	}

	log.Info("dockerfile lint passed")
	return out, nil
}

// ---------------------------------------------------------------------------
// Test
// ---------------------------------------------------------------------------

// Test runs language-specific unit tests inside the buildContainer from globalconfig.
func (m *CiModule) Test(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	language string,
) (string, error) {
	log := logger("test")
	log.Info("starting tests", "language", language)

	bc, err := loadContext(ctx, globalConfig, repoConfig, "", "")
	if err != nil {
		return "", fmt.Errorf("test: %w", err)
	}
	bc.Language = language

	cmd := bc.TestCommand()
	if cmd == "" {
		log.Info("no test command configured, skipping", "language", language)
		return "skipped: no test command for " + language, nil
	}

	image := resolveBuildContainer(bc)
	log.Info("running tests", "image", image, "command", cmd)

	container := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"sh", "-c", cmd})

	out, err := container.Stdout(ctx)
	if err != nil {
		if bc.TestIgnoreFailure() {
			log.Warn("tests failed but ignoreFailure=true", "error", err)
			return "tests failed (ignored): " + err.Error(), nil
		}
		log.Error("tests failed", "error", err)
		return "", fmt.Errorf("tests failed: %w", err)
	}

	if language == "golang" {
		if bc.Global.Defaults.UnitTests.Golang.Datarace.Enabled {
			log.Info("running data race detection")
			_, raceErr := container.
				WithExec([]string{"sh", "-c", bc.Global.Defaults.UnitTests.Golang.Datarace.Command}).
				Stdout(ctx)
			if raceErr != nil {
				log.Warn("data race detection found issues", "error", raceErr)
			}
		}
		if bc.Global.Defaults.UnitTests.Golang.Benchmark.Enabled {
			log.Info("running benchmarks")
			_, benchErr := container.
				WithExec([]string{"sh", "-c", bc.Global.Defaults.UnitTests.Golang.Benchmark.Command}).
				Stdout(ctx)
			if benchErr != nil {
				log.Warn("benchmarks failed", "error", benchErr)
			}
		}
	}

	log.Info("tests passed")
	return out, nil
}

// ---------------------------------------------------------------------------
// Security Scans
// ---------------------------------------------------------------------------

// Semgrep runs a semgrep scan using the semgrep image from globalconfig.
func (m *CiModule) Semgrep(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	// +optional
	// +default=""
	rulesPath string,
) (string, error) {
	log := logger("semgrep")
	log.Info("starting semgrep scan")

	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return "", fmt.Errorf("semgrep: %w", err)
	}

	image := global.Defaults.Semgrep
	log.Info("using image", "image", image)

	args := []string{"semgrep", "scan", "--config=auto", "."}
	if rulesPath != "" {
		args = []string{"semgrep", "scan", "--config", rulesPath, "."}
	}

	out, err := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec(args).
		Stdout(ctx)

	if err != nil {
		log.Error("semgrep scan failed", "error", err)
		return "", fmt.Errorf("semgrep scan failed: %w", err)
	}

	log.Info("semgrep scan passed")
	return out, nil
}

// SecretScan runs trivy secret scanning using trivyContainer from globalconfig.
func (m *CiModule) SecretScan(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
) (string, error) {
	log := logger("secret-scan")
	log.Info("starting secret scan")

	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return "", fmt.Errorf("secret-scan: %w", err)
	}

	image := global.Defaults.TrivyContainer
	log.Info("using image", "image", image)

	out, err := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"trivy", "fs", "--scanners", "secret", "--exit-code", "1", "."}).
		Stdout(ctx)

	if err != nil {
		log.Error("secret scan failed", "error", err)
		return "", fmt.Errorf("secret scan failed: %w", err)
	}

	log.Info("secret scan passed")
	return out, nil
}

// MisconfigScan runs trivy misconfiguration scanning using trivyContainer from globalconfig.
func (m *CiModule) MisconfigScan(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
) (string, error) {
	log := logger("misconfig-scan")
	log.Info("starting misconfiguration scan")

	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return "", fmt.Errorf("misconfig-scan: %w", err)
	}

	image := global.Defaults.TrivyContainer
	log.Info("using image", "image", image)

	out, err := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"trivy", "fs", "--scanners", "misconfig", "--exit-code", "1", "."}).
		Stdout(ctx)

	if err != nil {
		log.Error("misconfiguration scan failed", "error", err)
		return "", fmt.Errorf("misconfiguration scan failed: %w", err)
	}

	log.Info("misconfiguration scan passed")
	return out, nil
}

// ImageScan runs trivy vulnerability scanning on a Docker image using trivyContainer from globalconfig.
func (m *CiModule) ImageScan(
	ctx context.Context,
	globalConfig *dagger.File,
	imageRef string,
	// +optional
	// +default=false
	trivyBypass bool,
) (string, error) {
	log := logger("image-scan")
	if trivyBypass {
		log.Info("trivy bypass enabled, skipping image scan")
		return "skipped: trivyBypass=true", nil
	}
	log.Info("scanning image", "image", imageRef)

	global, err := LoadGlobalConfig(ctx, globalConfig)
	if err != nil {
		return "", fmt.Errorf("image-scan: %w", err)
	}

	trivyImage := global.Defaults.TrivyContainer
	log.Info("using trivy image", "trivyImage", trivyImage)

	out, err := dag.Container().
		From(trivyImage).
		WithExec([]string{"trivy", "image", "--severity", "HIGH,CRITICAL", imageRef}).
		Stdout(ctx)

	if err != nil {
		log.Error("image scan failed", "error", err)
		return "", fmt.Errorf("image scan failed: %w", err)
	}

	log.Info("image scan passed")
	return out, nil
}

// ---------------------------------------------------------------------------
// Build (non-Docker artifact build)
// ---------------------------------------------------------------------------

// Build runs a language-specific build command inside the buildContainer from globalconfig.
func (m *CiModule) Build(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	language string,
	// +optional
	// +default=""
	buildCommand string,
) (string, error) {
	log := logger("build")
	log.Info("starting build", "language", language)

	bc, err := loadContext(ctx, globalConfig, repoConfig, "", "")
	if err != nil {
		return "", fmt.Errorf("build: %w", err)
	}
	bc.Language = language

	cmd := buildCommand
	if cmd == "" {
		if langCmd, ok := bc.Global.Defaults.Build[language]; ok {
			cmd = langCmd.Command
		}
	}
	if cmd == "" {
		log.Info("no build command configured, skipping")
		return "skipped: no build command", nil
	}

	image := resolveBuildContainer(bc)
	log.Info("running build", "image", image, "command", cmd)

	out, err := dag.Container().
		From(image).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"sh", "-c", cmd}).
		Stdout(ctx)

	if err != nil {
		log.Error("build failed", "error", err)
		return "", fmt.Errorf("build failed: %w", err)
	}

	log.Info("build succeeded")
	return out, nil
}

// ---------------------------------------------------------------------------
// Push (Docker build + ECR push)
// ---------------------------------------------------------------------------

// Push builds a Docker image and pushes to ECR based on parent/service routing from globalconfig.
func (m *CiModule) Push(
	ctx context.Context,
	src *dagger.Directory,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	branch string,
	commitSHA string,
	// +optional
	// +default="Dockerfile"
	dockerfile string,
	// +optional
	// +default=""
	version string,
) (string, error) {
	log := logger("push")

	bc, err := loadContext(ctx, globalConfig, repoConfig, branch, commitSHA)
	if err != nil {
		return "", fmt.Errorf("push: %w", err)
	}
	if version != "" {
		bc.Version = version
	}

	log.Info("starting push",
		"parent", bc.Parent,
		"service", bc.ServiceName,
		"branch", branch,
		"commit", commitSHA,
	)

	ecrURLs, err := bc.ResolveECRURLs()
	if err != nil {
		return "", fmt.Errorf("push: %w", err)
	}

	tags := generateTags(branch, commitSHA, version)
	log.Info("generated tags", "tags", strings.Join(tags, ", "))

	image := src.DockerBuild(dagger.DirectoryDockerBuildOpts{Dockerfile: dockerfile})

	var results []string
	for _, ecrURL := range ecrURLs {
		for _, parent := range strings.Split(bc.Parent, ",") {
			parent = strings.TrimSpace(parent)
			for _, service := range strings.Split(bc.ServiceName, ",") {
				service = strings.TrimSpace(service)
				for _, tag := range tags {
					ref := fmt.Sprintf("%s/plivo/%s/%s:%s", ecrURL, parent, service, tag)
					log.Info("pushing image", "ref", ref)

					_, pushErr := image.Publish(ctx, ref)
					if pushErr != nil {
						log.Error("failed to push", "ref", ref, "error", pushErr)
						return "", fmt.Errorf("push failed for %s: %w", ref, pushErr)
					}
					results = append(results, ref)
				}
			}
		}
	}

	log.Info("push completed", "images", len(results))
	return fmt.Sprintf("pushed %d images:\n%s", len(results), strings.Join(results, "\n")), nil
}

func generateTags(branch, commitSHA, version string) []string {
	safeBranch := strings.ReplaceAll(branch, "/", "_")
	shortSHA := commitSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	tags := []string{
		safeBranch,
		shortSHA,
		safeBranch + "-" + shortSHA + "-" + ts,
	}

	if branch == "master" || branch == "main" {
		if version != "" {
			tags = append(tags, version)
			parts := strings.Split(version, ".")
			if len(parts) > 1 {
				tags = append(tags, strings.Join(parts[:len(parts)-1], "."))
			}
		}
		tags = append(tags, "latest")
	} else if version != "" {
		tags = append(tags, safeBranch+"-"+version)
		parts := strings.Split(version, ".")
		if len(parts) > 1 {
			tags = append(tags, safeBranch+"-"+strings.Join(parts[:len(parts)-1], "."))
		}
	}

	return tags
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// Deploy triggers a deployment based on the deploy method in the repo config.
func (m *CiModule) Deploy(
	ctx context.Context,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	deployEnv string,
	version string,
	// +optional
	// +default=""
	branch string,
) (string, error) {
	log := logger("deploy")
	log.Info("starting deploy", "env", deployEnv, "version", version)

	bc, err := loadContext(ctx, globalConfig, repoConfig, branch, "")
	if err != nil {
		return "", fmt.Errorf("deploy: %w", err)
	}

	envConfig, ok := bc.Repo.Deploy[deployEnv]
	if !ok {
		return "", fmt.Errorf("deploy: no deploy config for env %q", deployEnv)
	}

	if len(envConfig.Regions) == 0 {
		return "", fmt.Errorf("deploy: no regions configured for env %q", deployEnv)
	}

	log.Info("deploy config",
		"method", envConfig.DeployMethod,
		"regions", envConfig.Regions,
		"parent", bc.Parent,
		"service", bc.ServiceName,
	)

	registryNames, ok := bc.Global.ECRRegistries[bc.Parent]
	if ok {
		firstName := strings.Split(registryNames, ",")[0]
		firstName = strings.TrimSpace(firstName)
		role := bc.ResolveIAMRole(firstName)
		log.Info("using IAM role", "role", role)
	}

	switch envConfig.DeployMethod {
	case "ecs":
		return m.deployECS(ctx, log, bc, envConfig, version)
	case "lambda":
		return m.deployLambda(ctx, log, bc, envConfig, version)
	case "s3site", "s3Website":
		return m.deployS3(ctx, log, bc, envConfig, version)
	case "ecsTask":
		return m.deployECSTask(ctx, log, bc, envConfig, version)
	default:
		return "", fmt.Errorf("deploy: unsupported deploy method %q", envConfig.DeployMethod)
	}
}

func (m *CiModule) deployECS(ctx context.Context, log *slog.Logger, bc *BuildContext, envConfig DeployEnvConfig, version string) (string, error) {
	log.Info("deploying via ECS")
	var results []string
	for _, region := range envConfig.Regions {
		log.Info("deploying to region", "region", region)
		out, err := dag.Container().
			From("amazon/aws-cli:latest").
			WithExec([]string{
				"aws", "ecs", "update-service",
				"--cluster", fmt.Sprintf("%s-%s", bc.Parent, bc.ServiceName),
				"--service", bc.ServiceName,
				"--force-new-deployment",
				"--region", region,
			}).Stdout(ctx)
		if err != nil {
			log.Error("ECS deploy failed", "region", region, "error", err)
			return "", fmt.Errorf("ECS deploy failed in %s: %w", region, err)
		}
		results = append(results, fmt.Sprintf("deployed to %s: %s", region, out))
	}
	log.Info("ECS deploy completed", "regions", len(envConfig.Regions))
	return strings.Join(results, "\n"), nil
}

func (m *CiModule) deployLambda(ctx context.Context, log *slog.Logger, bc *BuildContext, envConfig DeployEnvConfig, version string) (string, error) {
	log.Info("deploying via Lambda")
	var results []string
	for _, region := range envConfig.Regions {
		out, err := dag.Container().
			From("amazon/aws-cli:latest").
			WithExec([]string{
				"aws", "lambda", "update-function-code",
				"--function-name", bc.ServiceName,
				"--image-uri", version,
				"--region", region,
			}).Stdout(ctx)
		if err != nil {
			log.Error("Lambda deploy failed", "region", region, "error", err)
			return "", fmt.Errorf("Lambda deploy failed in %s: %w", region, err)
		}
		results = append(results, fmt.Sprintf("deployed to %s: %s", region, out))
	}
	log.Info("Lambda deploy completed")
	return strings.Join(results, "\n"), nil
}

func (m *CiModule) deployS3(ctx context.Context, log *slog.Logger, bc *BuildContext, envConfig DeployEnvConfig, version string) (string, error) {
	log.Info("deploying via S3")
	var results []string
	for _, region := range envConfig.Regions {
		out, err := dag.Container().
			From("amazon/aws-cli:latest").
			WithExec([]string{
				"aws", "s3", "sync",
				".", fmt.Sprintf("s3://%s-%s-deploy", bc.Parent, bc.ServiceName),
				"--region", region,
				"--delete",
			}).Stdout(ctx)
		if err != nil {
			log.Error("S3 deploy failed", "region", region, "error", err)
			return "", fmt.Errorf("S3 deploy failed in %s: %w", region, err)
		}
		results = append(results, fmt.Sprintf("deployed to %s: %s", region, out))
	}
	log.Info("S3 deploy completed")
	return strings.Join(results, "\n"), nil
}

func (m *CiModule) deployECSTask(ctx context.Context, log *slog.Logger, bc *BuildContext, envConfig DeployEnvConfig, version string) (string, error) {
	log.Info("deploying ECS task")
	var results []string
	for _, region := range envConfig.Regions {
		out, err := dag.Container().
			From("amazon/aws-cli:latest").
			WithExec([]string{
				"aws", "ecs", "run-task",
				"--cluster", fmt.Sprintf("%s-%s", bc.Parent, bc.ServiceName),
				"--task-definition", bc.ServiceName,
				"--region", region,
			}).Stdout(ctx)
		if err != nil {
			log.Error("ECS task deploy failed", "region", region, "error", err)
			return "", fmt.Errorf("ECS task deploy failed in %s: %w", region, err)
		}
		results = append(results, fmt.Sprintf("ran task in %s: %s", region, out))
	}
	log.Info("ECS task deploy completed")
	return strings.Join(results, "\n"), nil
}

// ---------------------------------------------------------------------------
// Notify
// ---------------------------------------------------------------------------

// Notify sends a Slack notification about the pipeline status.
func (m *CiModule) Notify(
	ctx context.Context,
	globalConfig *dagger.File,
	repoConfig *dagger.File,
	status string,
	// +optional
	// +default=""
	deployEnv string,
	// +optional
	// +default=""
	failedStage string,
	// +optional
	// +default=""
	slackWebhookURL string,
) (string, error) {
	log := logger("notify")

	bc, err := loadContext(ctx, globalConfig, repoConfig, "", "")
	if err != nil {
		return "", fmt.Errorf("notify: %w", err)
	}

	channel := bc.SlackChannel(deployEnv)
	log.Info("sending notification",
		"status", status,
		"channel", channel,
		"service", bc.ServiceName,
		"failedStage", failedStage,
	)

	if slackWebhookURL == "" {
		log.Warn("no slack webhook URL provided, skipping notification")
		return "skipped: no webhook URL", nil
	}

	message := fmt.Sprintf(`{"channel":"%s","text":"[%s] %s/%s - %s"}`,
		channel, status, bc.Parent, bc.ServiceName, failedStage)

	out, err := dag.Container().
		From("curlimages/curl:latest").
		WithExec([]string{
			"curl", "-s", "-X", "POST",
			"-H", "Content-Type: application/json",
			"-d", message,
			slackWebhookURL,
		}).Stdout(ctx)

	if err != nil {
		log.Error("notification failed", "error", err)
		return "", fmt.Errorf("notification failed: %w", err)
	}

	log.Info("notification sent")
	return out, nil
}
