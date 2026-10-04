resource "cosmoner_webhook_endpoint" "deploys" {
  name   = "Deploy notifications"
  url    = "https://hooks.example.com/cosmoner"
  events = ["app.deployed", "app.failed"]
}

# Hand the signing secret to whatever verifies the deliveries.
resource "cosmoner_secret" "webhook_signing_secret" {
  name  = "COSMONER_WEBHOOK_SECRET"
  value = cosmoner_webhook_endpoint.deploys.signing_secret
}
