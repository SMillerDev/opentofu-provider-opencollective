resource "opencollective_collective" "example" {
  name             = "My Project"
  slug             = "my-project"
  description      = "Sustaining my open source project"
  long_description = "<p>A longer story about the project.</p>"
  tags             = ["open source", "go"]
  currency         = "USD"

  # Apply to a fiscal host. The host must approve the application;
  # see host_application_status.
  host_slug = "opensource"

  social_links = [
    { type = "WEBSITE", url = "https://example.com" },
    { type = "GITHUB", url = "https://github.com/example/my-project" },
  ]

  # Collectives with financial activity cannot be deleted; archive instead.
  archive_on_destroy = true
}
