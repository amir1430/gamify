FROM centrifugo/centrifugo:v6.9.7

# config.json is generated from gamify.yaml (gamify-config) and mounted at /centrifugo/config.json:
# a file in Compose, a ConfigMap in Kubernetes. It's not baked in, so one image serves every brand.
# Secrets come from env, never from config.json:
#   CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY  = the app's centrifugo.token_secret
#   CENTRIFUGO_GRPC_API_KEY                  = the app's centrifugo.api_key
EXPOSE 8000 10000
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
    CMD wget -qO- http://localhost:8000/health || exit 1
CMD ["centrifugo", "--config=/centrifugo/config.json"]
