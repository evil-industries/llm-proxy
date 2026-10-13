# Permanent log archives

Log archival preserves completed application, request/response, and error logs in
an S3-compatible bucket. Archives are gzip files. The server verifies uploaded
bytes by reading the object back and checking its SHA-256 digest before removing
the local copy. It never deletes archived S3 objects.

## Configuration

Create a private bucket and disable expiration rules for the archive prefix.
Configure the proxy:

```yaml
logging-to-file: true
request-log: true
log-archive:
  enabled: true
  endpoint: "https://s3.amazonaws.com"
  bucket: "my-proxy-log-archive"
  region: "eu-central-1"
  prefix: "production/proxy-1"
```

`logging-to-file` captures application logs. `request-log` captures detailed
successful requests and responses; when it is disabled, forced error logs are
still archived. Archival does not enable either capture setting automatically.
Request logs can contain private prompts, responses, and headers: restrict access
to the bucket and the management console accordingly.

Use AWS environment credentials (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
and optional `AWS_SESSION_TOKEN`) or an IAM role. Alternatively, set
`access-key-id`, `secret-access-key`, and optional `session-token` within
`log-archive`. These fields are omitted from configuration JSON. For a custom
S3 endpoint, set `endpoint` and, if required, `path-style: true`. HTTPS is the
default; an explicit `http://` endpoint supports local S3-compatible services.

The archive identity needs `s3:ListBucket` on the bucket and `s3:PutObject` and
`s3:GetObject` on the selected prefix. Multipart uploads may additionally need
`s3:AbortMultipartUpload` and `s3:ListMultipartUploadParts`. SSE-KMS buckets also
need the corresponding KMS permissions. The application does not create buckets,
change lifecycle policies, or need `s3:DeleteObject`.

Use a distinct prefix per deployment. Objects use a content digest and the
original filename, making retries idempotent while preserving different content
even when filenames coincide.

For Docker deployments, pass AWS credential environment variables into the proxy
container explicitly; variables on the host alone are not inherited. In Home
mode, the archive destination and credentials remain node-local configuration;
Home overlays do not replace them. Home still controls the existing log capture
settings, and enabled request logging continues forwarding logs to Home as well
as preserving a local copy for S3. No Home protocol change is required.

## Upload lifecycle

The background worker archives completed request/error files and application logs
after rotation. `main.log` remains local while active; application logs currently
rotate at 10 MB. Uploading and verification stream bytes with bounded memory.
Compression needs temporary disk space. Failed uploads or failed integrity checks
leave the source log available for retry, including after a restart.

After a restart, recognized orphaned request bodies, log fragments, and unfinished
log files are preserved as separately downloadable `recovered-*` archives. They
contain the bytes captured before interruption, rather than a reconstructed
complete request. Files still being written in the current process are never
treated as crash leftovers.

While archival is enabled, `logs-max-total-size-mb`, `error-logs-max-files`, and
the management clear-logs action cannot discard unarchived history. No archive
age or count limit is imposed in S3. Disabling archival restores the legacy
retention settings, so set both limits to `0` before disabling it if pending
local logs must remain indefinitely.

An S3 outage can still fill the staging disk: monitor free space and upload
warnings, and provision capacity for your expected outage window. The server
does not silently discard logs to enforce a local storage budget. Docker stdout
logs are managed separately by Docker and are not included in this archive.

## Downloads

The management console's Logs page provides an archive download list covering
local files and S3 objects. S3 downloads are `.gz` attachments; decompress with
`gzip -dc archive.log.gz` or a compatible archive utility. Downloads stream
through the authenticated management API, so bucket credentials are never sent
to the browser.

The API endpoints are:

- `GET /v0/management/log-archives?limit=100&cursor=...`: a paginated list of
  local files followed by S3 archives. Follow the returned cursor for more files.
- `GET /v0/management/log-archives/download?source=local&key=...`: download a
  local file using the entry's key.
- `GET /v0/management/log-archives/download?source=s3&key=...`: download a
  compressed S3 object using the entry's key.

URL-encode cursor and key query values. Existing log-viewing and request-ID
download endpoints continue to operate on local files; use the archive list to
download a file after it has moved to S3. Compressed archives are intended for
download, with no inline archive viewer.
