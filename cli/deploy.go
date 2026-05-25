package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy [dir]",
	Short: "Package a directory and deploy it",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDeploy,
}

var (
	deployPrevious string
	deploySecrets  []string
	deployWait     bool
)

func init() {
	deployCmd.Flags().StringVar(&deployPrevious, "previous", "", "Previous deployment ID to redeploy")
	deployCmd.Flags().StringSliceVar(&deploySecrets, "secret", nil, "Secrets as KEY=VALUE (repeatable)")
	deployCmd.Flags().BoolVar(&deployWait, "wait", true, "Wait for deployment to become ready")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	manifestPath := filepath.Join(dir, "raziel.yaml")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		manifestPath = filepath.Join(dir, "runtm.yaml")
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("manifest not found — create raziel.yaml in %s", dir)
	}

	fmt.Fprintf(os.Stderr, "Packaging %s...\n", dir)
	artifact, err := tarDir(dir)
	if err != nil {
		return fmt.Errorf("package: %w", err)
	}

	secrets := map[string]string{}
	for _, kv := range deploySecrets {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			secrets[parts[0]] = parts[1]
		}
	}
	secretsJSON, _ := json.Marshal(secrets)

	// Build multipart form
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	addField(mw, "manifest", "manifest.yaml", manifestData)
	addField(mw, "artifact", "artifact.tar.gz", artifact)
	if deployPrevious != "" {
		mw.WriteField("previous_deployment_id", deployPrevious) //nolint:errcheck
	}
	mw.WriteField("secrets", string(secretsJSON)) //nolint:errcheck
	mw.Close()

	c := newClient()
	req, _ := http.NewRequest(http.MethodPost, c.base+"/v0/deployments", &body)
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	result, err := decodeResponse(resp)
	if err != nil {
		return err
	}

	depID, _ := result["deployment_id"].(string)
	fmt.Fprintf(os.Stderr, "Deployment created: %s\n", depID)

	if !deployWait {
		outputJSON(result)
		return nil
	}

	// Poll until ready or failed
	fmt.Fprintf(os.Stderr, "Waiting for deployment to become ready")
	for i := 0; i < 120; i++ {
		time.Sleep(5 * time.Second)
		fmt.Fprintf(os.Stderr, ".")
		current, err := c.get("/v0/deployments/" + depID)
		if err != nil {
			continue
		}
		state, _ := current["state"].(string)
		if state == "ready" || state == "failed" || state == "destroyed" {
			fmt.Fprintln(os.Stderr)
			outputJSON(current)
			return nil
		}
	}
	fmt.Fprintln(os.Stderr, "\ntimeout waiting for deployment")
	return nil
}

func addField(mw *multipart.Writer, field, filename string, data []byte) {
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return
	}
	io.Copy(part, bytes.NewReader(data)) //nolint:errcheck
}

func tarDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		// Skip hidden and common junk
		base := filepath.Base(rel)
		if strings.HasPrefix(base, ".") || base == "node_modules" || base == "__pycache__" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	tw.Close()
	gw.Close()
	return buf.Bytes(), nil
}
