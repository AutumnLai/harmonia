package util

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"regexp"

	"go.uber.org/zap"
)

var baseDir string

func init() {

	createCredHelperScript()
	out, err := execCommand("git", []string{"config", "--global", "user.email", "operator@harmonia"}, ".", []string{})
	if err != nil {
		zap.L().Warn("git config setup error", zap.String("output", out), zap.Error(err))
	}
	_, err = execCommand("git", []string{"config", "--global", "user.name", "Harmonia Operator"}, ".", []string{})
	if err != nil {
		zap.L().Warn("git config setup error", zap.String("output", out), zap.Error(err))
	}

	baseDir = "/repos/"
}

func createCredHelperScript() {
	txt := []byte("printf '%s\\n' " + Config.GitUserToken)
	err := ioutil.WriteFile("./credentialHelper.sh", txt, 0710)
	if err != nil {
		zap.L().Error("create credential helper error", zap.Error(err))
	}
	zap.L().Debug("create credential helper script complete")
}

func GitHttpURLToRepoFullName(gitHttpURL string) string {
	// Modified https://regex101.com/library/BuA5xF
	re := regexp.MustCompile(`(?P<method>https?):\/\/(?:\w+@)(?P<provider>.*?(?P<port>\:\d+)?)(?:\/|:)(?P<handle>(?P<owner>.+?)\/(?P<repo>.+?))(?:\.git|\/)?$`)
	return re.ReplaceAllString(gitHttpURL, "${owner}/${repo}")
}

func CloneRepository(gitHttpURL string) error {
	localRepoPath := baseDir + GitHttpURLToRepoFullName(gitHttpURL)
	if _, err := os.Stat(localRepoPath + "/.git"); !os.IsNotExist(err) {
		zap.L().Debug(fmt.Sprintf("Repo [%v] existed, skipped clone [%v]", localRepoPath, gitHttpURL))
		return nil
	}

	zap.L().Debug(fmt.Sprintf("Cloning Data from [%v] [%v]...", gitHttpURL, localRepoPath))
	os.MkdirAll(localRepoPath, 0755)

	_, err := execGitPasswordCommand([]string{"clone", gitHttpURL, localRepoPath}, localRepoPath)

	if err != nil {
		zap.L().Fatal(fmt.Sprintf("Clone fail [%v]", err))
	}

	return err
}

func PullData(gitHttpURL string) error {
	zap.L().Info(fmt.Sprintf("Pulling Data from [%v]...", gitHttpURL))

	localRepoPath := baseDir + GitHttpURLToRepoFullName(gitHttpURL)

	_, err := execGitPasswordCommand([]string{"pull"}, localRepoPath)
	if err != nil {
		return err
	}

	zap.L().Info("Pull Succeed")
	return nil
}

func PushUpdates(gitHttpURL string, tag string) error {
	localRepoPath := baseDir + GitHttpURLToRepoFullName(gitHttpURL)

	lfsCheck(localRepoPath)

	_, err := execCommand("git", []string{"add", "."}, localRepoPath, []string{})
	if err != nil {
		return err
	}

	_, err = execCommand("git", []string{"commit", "-m", "Harmonia Model Update", "--allow-empty"}, localRepoPath, []string{})
	if err != nil {
		return err
	}

	if tag != "" {
		_, err = execCommand("git", []string{"tag", tag}, localRepoPath, []string{})
		if err != nil {
			return err
		}
	}

	zap.L().Info(fmt.Sprintf("Pushing Data to [%v]...", gitHttpURL))

	env := os.Environ()
	credPath, err := filepath.Abs("./credentialHelper.sh")
	if err != nil {
		zap.L().Fatal("get path of credential helper script error", zap.Error(err))
	}
	env = append(env, "GIT_ASKPASS="+credPath)
	_, err = execCommand("git", []string{"push"}, localRepoPath, env)
	if err != nil {
		return err
	}

	if tag != "" {
		_, err := execCommand("git", []string{"push", "origin", tag}, localRepoPath, env)
		if err != nil {
			return err
		}
	}

	zap.L().Info("Push Succeed")
	return nil
}

func GetTrainPlanData() (*TrainPlan, error) {
	zap.L().Info("get train plan data...")
	PullData(Config.TrainPlanRepo.GitHttpURL)
	data, err := ioutil.ReadFile(
		// TODO: get filename of plan by argument ?
		filepath.Join(baseDir + GitHttpURLToRepoFullName(Config.TrainPlanRepo.GitHttpURL), "plan.json"))
	if err != nil {
		zap.L().Fatal("cannot read file", zap.Error(err))
	}
	var plan TrainPlan
	err = json.Unmarshal(data, &plan)
	if err != nil {
		zap.L().Fatal("unmarshal json error", zap.Error(err))
		return nil, err
	}
	zap.L().Debug("", zap.String("train plan", fmt.Sprintf("%v", plan)))
	return &plan, nil
}

func lfsCheck(repoPath string) {
	filepath.Walk(repoPath, processFile(repoPath))
}

func processFile(repoPath string) filepath.WalkFunc {
	return func(path string, f os.FileInfo, err error) error {
		if err != nil {
			zap.L().Fatal("", zap.Error(err))
		}

		// ignore .git directory
		if f.IsDir() && f.Name() == ".git" {
			return filepath.SkipDir
		}

		switch mode := f.Mode(); {
		case mode.IsDir():
			// skip directory
			return nil
		case mode.IsRegular():
			if !isTextFile(path) {
				err := lfsTrackFile(f.Name(), repoPath)
				if err != nil {
					zap.L().Fatal("git lfs track file error", zap.Error(err))
				}
			}
		}
		return nil
	}
}

func isTextFile(path string) bool {
	out, err := execCommand("file", []string{path}, ".", []string{})
	if err != nil {
		zap.L().Fatal("run file command fail", zap.Error(err))
	}
	return strings.Contains(out, "text")
}

func lfsTrackFile(filename string, repoPath string) error {
	zap.L().Info("git lfs track file", zap.String("file", filename))
	_, err := execCommand("git", []string{"lfs", "track", filename}, repoPath, []string{})
	if err != nil {
		return err
	}
	return nil
}

func execGitPasswordCommand(args []string, path string) (string, error) {
	env := os.Environ()
	credPath, err := filepath.Abs("./credentialHelper.sh")
	if err != nil {
		zap.L().Fatal("get path of credential helper script error", zap.Error(err))
	}
	env = append(env, "GIT_ASKPASS="+credPath)
	return execCommand("git", args, path, env)
}

func execCommand(name string, args []string, path string, env []string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if path != "" {
		cmd.Dir = path
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	err := cmd.Run()
	zap.L().Debug("exec command",
		zap.String("command", name+" "+strings.Join(args, " ")),
		zap.String("output", stdout.String()))
	if err != nil {
		return stderr.String(), err
	}

	return stdout.String(), nil
}