FROM python:3.12-slim

# Install only the minimal runtime dependencies.
RUN pip install --no-cache-dir requests

WORKDIR /workspace

# The state file is bind-mounted here at runtime by the DockerRunner.
# The agent reads STATE_FILE (default: /workspace/state.json), updates it,
# and writes the result back to the same path.
ENV STATE_FILE=/workspace/state.json

COPY agent.py /agent.py

CMD ["python3", "/agent.py"]
