resource "cosmoner_resource_group" "production" {
  name = "Production"
}

resource "cosmoner_resource_group" "backend" {
  name      = "Backend"
  parent_id = cosmoner_resource_group.production.id
}
