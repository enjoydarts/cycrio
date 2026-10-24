# Wrangler/workerd requires glibc; use Debian rather than musl-based Alpine.
FROM node:26.12.1-trixie-slim AS development
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /app
RUN npm install --global npm@12.2.1
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
EXPOSE 5173
CMD ["npm", "run", "dev"]

FROM development AS build
RUN npm run build
