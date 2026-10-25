# Built from a repository. `apply` returns once the first deploy is running.
resource "cosmoner_app" "api" {
  name          = "api"
  size          = "shared-s"
  region        = "se-sto"
  git_provider  = "github"
  git_repo      = "acme/api"
  git_branch    = "main"
  auto_deploy   = true
  public_port   = 3000
  build_command = "npm run build"
  run_command   = "npm start"
}

# Run from a public image.
resource "cosmoner_app" "proxy" {
  name               = "proxy"
  size               = "shared-xs"
  region             = "se-sto"
  container_registry = "dockerhub"
  container_image    = "nginx:1.27"
  container_port     = 80

  timeouts {
    create = "10m"
  }
}

output "api_url" {
  value = cosmoner_app.api.url
}
