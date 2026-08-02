# Security

Please do not report vulnerabilities in public issues.

Use GitHub's private vulnerability reporting for this repository. Include the affected version, a minimal reproduction, the expected impact, and any suggested mitigation. Do not include production credentials, private knowledge content, or identity tokens in the report.

The `header` identity provider is a development convenience and must not be exposed to untrusted clients. Production deployments should use the remote identity provider behind TLS and treat `X-Acts-For` as a privileged delegation claim.
