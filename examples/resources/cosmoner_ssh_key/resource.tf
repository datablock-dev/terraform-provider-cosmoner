resource "cosmoner_ssh_key" "laptop" {
  name       = "Laptop"
  public_key = file("~/.ssh/id_ed25519.pub")
}
