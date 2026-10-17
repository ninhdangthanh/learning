#!/usr/bin/env bash
set -uo pipefail

region="${AWS_REGION:-ap-southeast-1}"

tg_name_map=$(aws elbv2 describe-target-groups --region "$region" --output json \
  | jq -c '[.TargetGroups[] | {key: .TargetGroupArn, value: .TargetGroupName}] | from_entries')

show_load_balancer() {
  aws elbv2 describe-load-balancers --load-balancer-arns "$1" --region "$region" --output json \
  | jq -r '.LoadBalancers[0] |
      "\n=== \(.LoadBalancerName) ===",
      "dns    : \(.DNSName)",
      "type   : \(.Type)   scheme=\(.Scheme)   state=\(.State.Code)",
      "vpc    : \(.VpcId)",
      "az     : \([.AvailabilityZones[].ZoneName] | sort | join(", "))",
      "sg     : \((.SecurityGroups // []) | join(", "))"'
}

show_rules() {
  aws elbv2 describe-rules --listener-arn "$1" --region "$region" --output json \
  | jq -r --argjson tg "$tg_name_map" '
      def target(a):
        (a.TargetGroupArn // (a.ForwardConfig.TargetGroups[0].TargetGroupArn? // null)) as $arn
        | if $arn == null then "?" else ($tg[$arn] // $arn) end;
      def render(a):
        if   a.Type == "forward"        then "forward -> \(target(a))"
        elif a.Type == "fixed-response" then "fixed-response \(a.FixedResponseConfig.StatusCode)  \(a.FixedResponseConfig.MessageBody // "")"
        elif a.Type == "redirect"       then "redirect \(a.RedirectConfig.StatusCode)"
        else a.Type end;
      def conds(r):
        (r.Conditions | map(.Values // []) | flatten | join("  "))
        | if . == "" then "(moi path)" else . end;
      .Rules
      | sort_by(if .Priority == "default" then 1000000 else (.Priority | tonumber) end)
      | .[]
      | "         \(.Priority | tostring | . + "        " | .[0:9]) \(conds(.) | . + "                                  " | .[0:34]) \(render(.Actions[0]))"'
}

show_listeners() {
  local listener
  for listener in $(aws elbv2 describe-listeners --load-balancer-arn "$1" --region "$region" \
                    --query 'Listeners[].ListenerArn' --output text); do
    aws elbv2 describe-listeners --listener-arns "$listener" --region "$region" --output json \
      | jq -r '.Listeners[0] | "\nlistener \(.Protocol):\(.Port)"'
    printf '         %-9s %-34s %s\n' "PRIORITY" "CONDITION" "ACTION"
    show_rules "$listener"
  done
}

for lb in $(aws elbv2 describe-load-balancers --region "$region" \
            --query 'LoadBalancers[].LoadBalancerArn' --output text); do
  show_load_balancer "$lb"
  show_listeners "$lb"
done
echo
