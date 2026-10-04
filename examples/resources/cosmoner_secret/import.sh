# <project_id>/<secret_id>, or just <secret_id> when the provider sets a project.
# The value cannot be imported: the next apply sets the configured one.
terraform import cosmoner_secret.database_password proj_123/clx0secret
