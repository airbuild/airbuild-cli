# Release signing

`checksums.txt` in every release is signed with an RSA-3072 key
(`checksums.txt.sig`). `install.sh`, `install.ps1` and `airbuild upgrade`
refuse a release whose signature doesn't verify against the public key in
`cmd/release-sign-pub.pem` (the same PEM is embedded in the binary and both
installers).

## Required secret

GitHub Actions secret `RELEASE_SIGNING_PRIVATE_KEY` — the RSA private key in
PEM form. The release workflow fails hard when it's missing.

## Rotating the key

1. `openssl genrsa -out new.pem 3072`
2. `openssl rsa -in new.pem -pubout -out cmd/release-sign-pub.pem`
3. Update the GitHub secret with `new.pem`.
4. Commit the new public key and release.

IMPORTANT: binaries built before the rotation can't verify releases signed
with the new key. Rotate only alongside a release, and expect older CLIs to
refuse upgrades signed by the new key (error message tells users to download
manually, which re-verifies with the current public key).
