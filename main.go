package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/segevfiner/dockerexec"
	"gopkg.in/yaml.v3"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]

	if len(args) < 1 {
		log.Fatal("Missing path to CI YAML file")
	}

	if len(args) < 2 {
		log.Fatal("Missing job name")
	}

	if len(args) < 3 {
		log.Fatal("Missing configuration")
	}

	// ---

	path := args[0]
	yamlFile, err := os.ReadFile(path)

	if err != nil {
		log.Fatal(err)
	}

	jobName := args[1]

	// ---

	var data map[string]interface{}
	err = yaml.Unmarshal(yamlFile, &data)

	if err != nil {
		log.Fatal(err)
	}

	// ---

	confFile, err := os.ReadFile(args[2])

	if err != nil {
		log.Fatal(err)
	}

	var conf configuration
	err = yaml.Unmarshal(confFile, &conf)

	// ---

	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)

	if err != nil {
		log.Fatal(err)
	}

	// ---

	// TODO: git include

	job, err := loadMap(&data, jobName)

	if err != nil {
		log.Fatal(err)
	}

	img := job["image"]

	if img == nil || img == "" {
		log.Fatal("Missing Docker image")
	}

	dimage := img.(string)

	for _, m := range conf.Docker.Mirrors {
		if strings.HasPrefix(dimage, m) {
			dimage = dimage[len(m)+1:]
			break
		}
	}

	ctx := context.Background()

	log.Printf("Making sure image %s is available ...", dimage)

	err = pullImage(&ctx, cli, dimage)

	fmt.Print("\n\n")
	log.Printf("Executing script from job %s ...\n", jobName)

	scriptKeys := []string{"before_script", "script", "after_script"}

	var script []string

	for _, k := range scriptKeys {
		v := job[k]

		if v == nil || v == "" {
			log.Printf("No job '%s'\n", k)
			continue
		}

		// ---

		if _, ok := v.(string); ok {
			script = append(script, v.(string))
		} else {
			lines := v.([]interface{})

			for _, ln := range lines {
				script = append(script, strings.TrimSpace(ln.(string)))
			}
		}
	}

	scriptFile, err := os.CreateTemp("", "script-*.sh")

	if err != nil {
		log.Fatal(err)
	}

	// TODO: defer os.Remove(scriptFile.Name())
	// TODO: Trap signal to force Docker cleanup

	_, err = scriptFile.WriteString(strings.Join(script, "\n"))

	if err != nil {
		log.Fatal(err)
	}

	i, err := cli.ImagePull(ctx, dimage, image.PullOptions{All: true})

	if err != nil {
		log.Fatal(err)
	}

	i.Close()

	// ---

	hostScript := scriptFile.Name()
	containerScript := fmt.Sprintf("/%s", filepath.Base(hostScript))

	err = os.Chmod(hostScript, 0722)

	if err != nil {
		log.Fatal(err)
	}

	cmd := dockerexec.Command(cli, dimage, "sh", "-c", containerScript)

	log.Println("Preparing environment variables ...")

	env := job["variables"].([]string)

	for _, e := range env {
		log.Printf("set %s\n", e)
	}

	fmt.Println()

	cmd.Config.Env = env
	cmd.Config.WorkingDir = "/tmp/repo"
	// TODO: container name
	// TODO: cache

	repoPath, err := filepath.Abs(filepath.Dir(path))

	if err != nil {
		log.Fatal(err)
	}

	cmd.HostConfig.Mounts = append(
		cmd.HostConfig.Mounts,
		mount.Mount{
			Type:     mount.TypeBind,
			Source:   repoPath,
			Target:   cmd.Config.WorkingDir,
			ReadOnly: false,
		},
	)

	cmd.HostConfig.Mounts = append(
		cmd.HostConfig.Mounts,
		mount.Mount{
			Type:     mount.TypeBind,
			Source:   hostScript,
			Target:   containerScript,
			ReadOnly: true,
		},
	)

	pipeTo := func(r io.Reader, w io.Writer) {
		scanner := bufio.NewScanner(r)

		scanner.Split(bufio.ScanLines)

		redirect := func() {
			for scanner.Scan() {
				fmt.Println(scanner.Text())
			}
		}

		go redirect()
	}

	pout, err := cmd.StdoutPipe()

	if err != nil {
		log.Fatal(err)
	}

	perr, err := cmd.StderrPipe()

	if err != nil {
		log.Fatal(err)
	}

	err = cmd.Start()

	if err != nil {
		log.Fatal(err)
	}

	pipeTo(pout, os.Stdout)
	pipeTo(perr, os.Stderr)

	err = cmd.Wait()

	// TODO: output artifacts

	if err != nil {
		log.Fatal(err)
	}
}

func loadMap(data *map[string]interface{}, name string) (map[string]interface{}, error) {
	m := (*data)[name]

	if m == nil {
		log.Fatalf("Reference not found: %s", name)
	}

	loaded := m.(map[string]interface{})

	env, err := loadEnv(&loaded)

	if err != nil {
		return nil, err
	}

	pn := loaded["extends"]

	if pn == nil {
		return loaded, nil
	}

	parentName := pn.(string)
	parent, err := loadMap(data, parentName)

	if err != nil {
		return nil, err
	}

	delete(loaded, "extends")

	img := parent["image"]

	if img != nil && img != "" {
		loaded["image"] = img
	}

	parentEnv, err := loadEnv(&parent)

	if err != nil {
		return nil, err
	}

	env = append(env, parentEnv...)

	loaded["variables"] = env

	// TODO: env
	scriptKeys := []string{"before_script", "script", "after_script"}

	for _, k := range scriptKeys {
		v := parent[k]

		if v == nil {
			continue
		}

		loaded[k] = v
	}

	return loaded, nil
}

func loadEnv(job *(map[string]interface{})) ([]string, error) {
	v := (*job)["variables"]

	if v == nil {
		return []string{}, nil
	}

	m, ok := v.(map[string]interface{})

	if ok {
		var entries []string

		for k, e := range m {
			value := e.(string)
			entries = append(entries, fmt.Sprintf("%s=%s", k, value))
		}

		return entries, nil
	}

	entries, ok := v.([]string)

	if !ok {
		return []string{}, nil
	}

	return entries, nil
}

type configuration struct {
	Docker dockerConfig `yaml:"docker"`
}

type dockerConfig struct {
	Mirrors []string `yaml:"mirrors"`
}

type pullMessage struct {
	Status   string `json:"status"`
	Progress string `json:"progress"`
}

func pullImage(
	ctx *context.Context,
	cli *client.Client,
	imageName string,
) error {
	out, err := cli.ImagePull(*ctx, imageName, image.PullOptions{})

	if err != nil {
		log.Fatal(err)
	}

	defer out.Close()

	fileScanner := bufio.NewScanner(out)

	fileScanner.Split(bufio.ScanLines)

	var msg pullMessage
	var lastStatus string = ""
	var lastProgress string

	for fileScanner.Scan() {
		err = json.Unmarshal([]byte(fileScanner.Text()), &msg)

		if err != nil {
			log.Fatal(err)
		}

		if msg.Status != lastStatus {
			if lastStatus != "" {
				fmt.Println()
			}

			if len(msg.Status) > 20 {
				fmt.Printf("\n%s", msg.Status)

				if msg.Progress != "" {
					fmt.Printf("\n%s", msg.Progress)
				}
			} else {
				fmt.Printf("%18s", msg.Status)

				if msg.Progress != "" {
					fmt.Printf(" %s", msg.Progress)
				}
			}

			lastStatus = msg.Status
		} else {
			n := len(lastProgress)

			for i := 0; i < n; i++ {
				fmt.Print("\b")
			}

			fmt.Print(msg.Progress)
		}

		lastProgress = msg.Progress
	}

	return err
}
