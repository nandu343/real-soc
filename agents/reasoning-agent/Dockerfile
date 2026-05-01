FROM python:3.12-slim

# Install only the minimal runtime dependency used by the agent.
RUN pip install --no-cache-dir requests

WORKDIR /workspace

# The state file is bind-mounted here at runtime by the DockerRunner.
ENV STATE_FILE=/workspace/state.json

COPY agent.py /workspace/agent.py
COPY prompt.md /workspace/prompt.md

CMD ["python3", "/workspace/agent.py"]
