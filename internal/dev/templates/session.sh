# ForceCommand passes the SFTP helper as $1, preserving paths with shell characters.
# OpenSSH otherwise rebuilds the subsystem command with incompatible shell quoting.
if [ "$SSH_ORIGINAL_COMMAND" = internal-sftp ]; then
  exec /bin/bash -c 'exec "$1"' flatpak-dev-sftp "$1";
fi

# Flatpak's account shell is /bin/sh. Select Bash to load the project profile.
if [ -n "$SSH_ORIGINAL_COMMAND" ]; then
  exec /bin/bash -c "$SSH_ORIGINAL_COMMAND";
fi
exec /bin/bash --rcfile "$BASH_ENV";
