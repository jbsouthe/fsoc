// Copyright 2022 Cisco Systems, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package solution

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/spf13/cobra"

	"github.com/cisco-open/fsoc/output"
	"github.com/cisco-open/fsoc/platform/api"
)

type ErrorItem struct {
	Error  string `json:"error"`
	Source string `json:"source"`
}

type Errors struct {
	Items []ErrorItem `json:"items"`
	Total int         `json:"total"`
}

type Result struct {
	Errors Errors `json:"errors"`
	Valid  bool   `json:"valid"`
}

func getSolutionValidateUrl() string {
	return "solnmgmt/v1beta/solutions"
}

func getSolutionValidateCmd() *cobra.Command {
	solutionValidateCmd.Flags().
		String("solution-bundle", "", "The fully qualified path name for the solution bundle .zip file that you want to validate")

	solutionValidateCmd.Flags().
		BoolP("bump", "b", false, "Increment the patch version before validation")
	solutionPushCmd.MarkFlagsMutuallyExclusive("solution-bundle", "bump")

	return solutionValidateCmd
}

var solutionValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate your solution package",
	Long: `This command allows the current tenant specified in the profile to upload the specified solution bundle for the purpose of validating its contents

Example:
  fsoc solution validate --solution-bundle=mysolution.zip`,
	Args:             cobra.ExactArgs(0),
	Run:              validateSolution,
	TraverseChildren: true,
}

func validateSolution(cmd *cobra.Command, args []string) {
	var manifestPath string
	var solutionArchivePath string
	solutionBundlePath, _ := cmd.Flags().GetString("solution-bundle")
	bumpFlag, _ := cmd.Flags().GetBool("bump")

	if solutionBundlePath == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			log.Fatal(err.Error())
		}
		manifestPath = currentDir
		if !isSolutionPackageRoot(manifestPath) {
			log.Fatalf("No solution manifest found in %q; please run this command in a folder with a solution or use the --solution-bundle flag", manifestPath)
		}
		manifest, err := getSolutionManifest(manifestPath)
		if err != nil {
			log.Fatalf("Failed to read the solution manifest in %q: %v", manifestPath, err)
		}
		if bumpFlag {
			if err := bumpManifestPatchVersion(manifest); err != nil {
				log.Fatal(err.Error())
			}
			if err := writeSolutionManifest(manifestPath, manifest); err != nil {
				log.Fatalf("Failed to update solution manifest in %q after version bump: %v", manifestPath, err)
			}
			output.PrintCmdStatus(cmd, fmt.Sprintf("Solution version updated to %v\n", manifest.SolutionVersion))
		}

		// create a temporary solution archive
		solutionArchive := generateZipNoCmd(manifestPath)
		solutionArchivePath = filepath.Base(solutionArchive.Name())
	} else {
		solutionArchivePath = solutionBundlePath
	}

	var message string

	file, err := os.Open(solutionArchivePath)
	if err != nil {
		log.Fatalf("Failed to open file %q: %v", solutionArchivePath, err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fw, err := writer.CreateFormFile("file", solutionArchivePath)
	if err != nil {
		log.Fatalf("Failed to create form file: %v", err)
	}

	_, err = io.Copy(fw, file)
	if err != nil {
		writer.Close()
		log.Fatalf("Failed to copy file %q into file writer: %v", solutionArchivePath, err)
	}

	writer.Close()

	headers := map[string]string{
		"stage":        "STABLE",
		"tag":          "stable",
		"operation":    "VALIDATE",
		"Content-Type": writer.FormDataContentType(),
	}

	var res Result

	err = api.HTTPPost(getSolutionValidateUrl(), body.Bytes(), &res, &api.Options{Headers: headers})
	if err != nil {
		log.Fatalf("Solution validate request failed: %v", err)
	}

	if res.Valid {
		message = fmt.Sprintf("Solution bundle %s validated successfully.\n", solutionArchivePath)
	} else {
		message = getSolutionValidationErrorsString(res.Errors.Total, res.Errors)
	}
	output.PrintCmdStatus(cmd, message)
	if !res.Valid {
		log.Fatalf("%d error(s) found while validating the solution", res.Errors.Total)
	}
}

func getSolutionValidationErrorsString(total int, errors Errors) string {
	var message = fmt.Sprintf("\n%d errors detected while validating solution package\n", total)
	for _, err := range errors.Items {
		message += fmt.Sprintf("- Error Content: %+v\n", err)
	}
	message += "\n"

	return message
}
