FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --silent
COPY . .
# Build-time UI language (en|tr) — read by Vite via import.meta.env in
# ui/src/shared/i18n/strings.ts. Set from the host's YUMMI_LANG (.yummi.conf)
# via docker-compose.yml's build.args; defaults to en for a bare `docker build`.
ARG VITE_YUMMI_LANG=en
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
