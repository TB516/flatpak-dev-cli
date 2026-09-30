set -eu;
# VS Code's probe misses the SDK's multiarch libstdc++ path.
touch /tmp/vscode-skip-server-requirements-check;

# Publish readiness after setup so callers can safely join the sandbox.
sed -n 's/^instance-id=//p' /.flatpak-info > "$1.tmp";
mv "$1.tmp" "$1";
exec sleep infinity;
