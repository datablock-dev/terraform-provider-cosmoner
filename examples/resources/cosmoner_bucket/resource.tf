resource "cosmoner_bucket" "assets" {
  name          = "assets"
  region        = "eu-north-1"
  tier          = "GROWTH"
  public_access = true
  public_paths  = ["images/", "downloads/"]
  cdn_enabled   = true

  cors_rules = [{
    allowed_origins = ["https://example.com"]
    allowed_methods = ["GET", "HEAD"]
    max_age_seconds = 3600
  }]
}

output "assets_cdn" {
  value = cosmoner_bucket.assets.cdn_domain
}
