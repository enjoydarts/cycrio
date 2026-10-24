# Wrangler/workerd requires glibc; use Debian rather than musl-based Alpine.
FROM node:22.23.4-bookworm-slim AS development
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
EXPOSE 5173
CMD ["npm", "run", "dev"]

FROM development AS build
RUN npm run build
