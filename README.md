# go-k8s-server

## set platforms

Ensure to set platforms for all architectures in github actions

```shell
- name: Build and push Docker image
uses: docker/build-push-action@v5
with:
  context: .
  file: ./Dockerfile
  push: true
  platforms: linux/amd64,linux/arm64 # Ensure to set for all architectures
```


```shell
# Test root endpoint
curl http://localhost:8080/

# Test health check endpoint
curl -i http://localhost:8080/health
```