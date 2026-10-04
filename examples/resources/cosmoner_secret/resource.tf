variable "database_password" {
  type      = string
  sensitive = true
}

# Terraform 1.11 and later: the value never reaches state or plan. Bump
# value_wo_version whenever the password changes.
resource "cosmoner_secret" "database_password" {
  name             = "DB_PASSWORD"
  environment      = "production"
  description      = "Primary database"
  value_wo         = var.database_password
  value_wo_version = 1
}

# Any Terraform version: the value is kept in state, marked sensitive.
resource "cosmoner_secret" "stripe_key" {
  name  = "STRIPE_SECRET_KEY"
  value = var.stripe_secret_key
}

variable "stripe_secret_key" {
  type      = string
  sensitive = true
}
