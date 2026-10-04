# The provider's default project.
data "cosmoner_project" "current" {}

output "project_name" {
  value = data.cosmoner_project.current.name
}
