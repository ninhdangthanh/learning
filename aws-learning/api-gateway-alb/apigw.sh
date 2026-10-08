#!/usr/bin/env bash
set -uo pipefail

region="${AWS_REGION:-ap-southeast-1}"

show_api() {
  aws apigatewayv2 get-api --api-id "$1" --region "$region" --output json \
  | jq -r '"\n=== \(.Name)  (\(.ApiId)) ===",
           "endpoint: \(.ApiEndpoint)",
           "protocol: \(.ProtocolType)   cors=\(if .CorsConfiguration then "on" else "off" end)"'
}

show_stages() {
  aws apigatewayv2 get-stages --api-id "$1" --region "$region" --output json \
  | jq -r '"stages  : " + ([.Items[] | "\(.StageName) (autoDeploy=\(.AutoDeploy // false))"] | join("  |  "))'
}

show_routes() {
  local integrations
  integrations=$(aws apigatewayv2 get-integrations --api-id "$1" --region "$region" --output json \
    | jq -c '[.Items[] | {key: .IntegrationId,
                          value: "\(.IntegrationType)  \(.IntegrationMethod // "-")  \(.IntegrationUri // "-")"}]
             | from_entries')
  printf '\nroutes  :\n'
  printf '          %-8s %-24s %s\n' "METHOD" "PATH" "INTEGRATION"
  aws apigatewayv2 get-routes --api-id "$1" --region "$region" --output json \
  | jq -r --argjson int "$integrations" '
      .Items
      | map(. + {method: (.RouteKey | split(" ")[0]),
                 path:   (.RouteKey | split(" ")[1] // "/")})
      | sort_by(.path, .method)
      | .[]
      | "          \(.method | . + "        " | .[0:9])" +
        "\(.path | . + "                         " | .[0:25])" +
        "\(($int[.Target | split("/") | last] // "(chua gan integration)"))"'
}

api_ids="${1:-}"
if [ -z "$api_ids" ]; then
  api_ids=$(aws apigatewayv2 get-apis --region "$region" --query 'Items[].ApiId' --output text)
fi

for api in $api_ids; do
  show_api "$api"
  show_stages "$api"
  show_routes "$api"
done
echo
