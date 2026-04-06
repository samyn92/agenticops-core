# Agent Runtime — Node.js container with Pi SDK
# Two entry points: agent-server.ts (daemon) and agent-task.ts (task)
# NOTE: Runs TypeScript source directly via tsx until Pi SDK API rewrite is done.

FROM node:22-slim

WORKDIR /app

# Install all dependencies (tsx needed at runtime for now)
COPY package.json package-lock.json* ./
RUN npm ci --ignore-scripts && npm cache clean --force

# Copy TypeScript source
COPY tsconfig.json ./
COPY src/ ./src/

# Create data directories
RUN mkdir -p /data/sessions /data/repos /data/scratch \
    && chown -R 1000:1000 /data

USER 1000:1000

# Default: daemon mode. Override CMD for task mode.
CMD ["npx", "tsx", "/app/src/agent-server.ts"]
