# syntax=docker/dockerfile:1

# Build from the generic Docker-enabled sandbox base. Atelier embeds the Docker
# Agent SDK in its own runner, so layering the separate Docker Agent CLI image
# would pull and store a second large binary that is never executed.
FROM docker/sandbox-templates:shell-docker

USER root
COPY --chown=agent:agent --chmod=0755 files/home/.local/lib/daw-runner /home/agent/.local/lib/daw-runner
USER agent
