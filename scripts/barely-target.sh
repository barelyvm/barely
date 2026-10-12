# Sourced by the test scripts: talk to barely's socket directly (never the
# shared Docker context, which Docker Desktop rewrites when it launches) and
# refuse to run unless the engine answering is barely's VM.
unset DOCKER_CONTEXT
export DOCKER_HOST="unix://${BARELY_HOME:-$HOME/.barely}/docker.sock"
engine=$(docker info --format '{{.Name}}' 2>/dev/null)
if [ "$engine" != "barely" ]; then
	echo "ABORT: $DOCKER_HOST answered as '${engine:-nothing}', not barely" >&2
	exit 1
fi
echo "INFO target: barely at $DOCKER_HOST"
