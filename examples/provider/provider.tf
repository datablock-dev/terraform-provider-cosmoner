terraform {
  required_providers {
    cosmoner = {
      source = "datablock-dev/cosmoner"
    }
  }
}

# Reads COSMONER_API_KEY, COSMONER_PROJECT_ID and COSMONER_API_URL from the
# environment, so nothing secret needs to be in configuration.
provider "cosmoner" {}
