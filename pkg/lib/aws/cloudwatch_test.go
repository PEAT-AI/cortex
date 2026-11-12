/*
Copyright 2022 Cortex Labs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/stretchr/testify/require"
)

// getAccountID must use the credentials of the client it is called on. A cortex CLI running with an
// IAM Identity Center (SSO) profile has its credentials in ~/.aws/config, which a bare
// session.NewSession() does not read, so resolving credentials again from the environment fails with
// NoCredentialProviders even though the client itself is authenticated.
func TestGetAccountIDUsesClientCredentials(t *testing.T) {
	// nothing in the environment or on disk can supply credentials
	missing := filepath.Join(t.TempDir(), "missing")
	for _, name := range []string{
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "AWS_SDK_LOAD_CONFIG", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", missing)
	t.Setenv("AWS_CONFIG_FILE", missing)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	var authHeader string
	fakeSTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:sts::123456789012:assumed-role/role/user</Arn>
    <UserId>AROAEXAMPLE:user</UserId>
    <Account>123456789012</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>00000000-0000-0000-0000-000000000000</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`))
	}))
	defer fakeSTS.Close()

	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String("us-west-2"),
		Endpoint:    aws.String(fakeSTS.URL),
		Credentials: credentials.NewStaticCredentials("AKIACLIENTKEY", "client-secret", ""),
		MaxRetries:  aws.Int(0),
	})
	require.NoError(t, err)

	client, err := NewForSession(sess)
	require.NoError(t, err)

	accountID, err := client.getAccountID()
	require.NoError(t, err)
	require.Equal(t, "123456789012", accountID)
	require.Contains(t, authHeader, "AKIACLIENTKEY", "request must be signed with the client's own credentials")
}
