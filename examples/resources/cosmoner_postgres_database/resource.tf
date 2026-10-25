resource "cosmoner_postgres_database" "main" {
  name    = "main"
  size    = "postgres-cosmoner-1vcpu-1gb"
  version = "17"
  region  = "se-sto"

  # Every argument replaces the database, and its data with it.
  lifecycle {
    prevent_destroy = true
  }
}

# Hand the connection string to the apps that use it.
resource "cosmoner_secret" "database_url" {
  name  = "DATABASE_URL"
  value = cosmoner_postgres_database.main.connection_uri
}
