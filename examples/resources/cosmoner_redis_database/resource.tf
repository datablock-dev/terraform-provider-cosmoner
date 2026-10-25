resource "cosmoner_redis_database" "cache" {
  name        = "cache"
  plan        = "valkey-1gb"
  region      = "se-sto"
  persistence = "AOF_EVERY_1_SECOND"
}

resource "cosmoner_secret" "redis_password" {
  name  = "REDIS_PASSWORD"
  value = cosmoner_redis_database.cache.password
}
