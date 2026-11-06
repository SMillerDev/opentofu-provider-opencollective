resource "opencollective_collective" "example" {
  name        = "My Project"
  slug        = "my-project"
  description = "Sustaining my open source project"
}

# Invites jane-doe as an admin. status is "INVITED" until she accepts.
resource "opencollective_collective_member" "jane" {
  collective_id = opencollective_collective.example.id
  member_slug   = "jane-doe"
  role          = "ADMIN"
  description   = "Lead maintainer"
  since         = "2024-01-01T00:00:00Z"
}

resource "opencollective_collective_member" "bookkeeper" {
  collective_id = opencollective_collective.example.id
  member_slug   = "acme-accounting"
  role          = "ACCOUNTANT"
}
