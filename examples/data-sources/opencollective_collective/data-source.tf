# Look up a collective by its slug (https://opencollective.com/babel)
data "opencollective_collective" "by_slug" {
  slug = "babel"
}

# Look up a collective by its public ID
data "opencollective_collective" "by_id" {
  id = "dgm9bnk8-0437xqry-ejpvzeol-jdayw5re"
}

# Look up a collective by its linked GitHub handle
data "opencollective_collective" "by_github" {
  github_handle = "babel/babel"
}

output "babel_host" {
  value = data.opencollective_collective.by_slug.host_slug
}
