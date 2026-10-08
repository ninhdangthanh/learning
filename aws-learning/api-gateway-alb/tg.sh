#!/usr/bin/env bash
set -uo pipefail

region="${AWS_REGION:-ap-southeast-1}"

resolve_arns() {
  if [ $# -gt 0 ] && [ -n "$1" ]; then
    aws elbv2 describe-target-groups --names "$1" --region "$region" \
      --query 'TargetGroups[].TargetGroupArn' --output text
  else
    aws elbv2 describe-target-groups --region "$region" \
      --query 'TargetGroups[].TargetGroupArn' --output text
  fi
}

show_config() {
  aws elbv2 describe-target-groups --target-group-arns "$1" --region "$region" \
    --query 'TargetGroups[0].[TargetGroupName,Protocol,Port,TargetType,HealthCheckPath,HealthCheckIntervalSeconds,HealthCheckTimeoutSeconds,HealthyThresholdCount,UnhealthyThresholdCount,Matcher.HttpCode,length(LoadBalancerArns)]' \
    --output text \
  | while read -r name proto port type path intv timeout up down code lbs; do
      printf '\n=== %s ===\n' "$name"
      printf 'listen  : %s:%s   target-type=%s\n' "$proto" "$port" "$type"
      printf 'probe   : %s moi %ss, timeout %ss, up %s / down %s, expect %s\n' \
             "$path" "$intv" "$timeout" "$up" "$down" "$code"
      printf 'gan LB  : %s\n' "$lbs"
    done
}

show_attributes() {
  aws elbv2 describe-target-group-attributes --target-group-arn "$1" --region "$region" \
    --query 'Attributes[?Key==`deregistration_delay.timeout_seconds`||Key==`load_balancing.algorithm.type`||Key==`stickiness.enabled`||Key==`slow_start.duration_seconds`].[Key,Value]' \
    --output text | awk '{printf "          %-40s %s\n", $1, $2}'
}

show_rules() {
  local tg="$1" lbs lb listener
  lbs=$(aws elbv2 describe-target-groups --target-group-arns "$tg" --region "$region" \
        --query 'TargetGroups[0].LoadBalancerArns' --output text)
  if [ -z "$lbs" ] || [ "$lbs" = "None" ]; then
    printf 'rule    : (mo coi - chua rule nao tro toi, create-service se fail)\n'
    return
  fi
  printf 'rule    :\n'
  for lb in $lbs; do
    for listener in $(aws elbv2 describe-listeners --load-balancer-arn "$lb" --region "$region" \
                      --query 'Listeners[].ListenerArn' --output text); do
      aws elbv2 describe-rules --listener-arn "$listener" --region "$region" --output json \
      | jq -r --arg tg "$tg" '
          .Rules[]
          | select(any(.Actions[]; .TargetGroupArn == $tg))
          | "          priority \(.Priority)   \((.Conditions[0].Values // ["(default action - bat moi path)"]) | join("   "))"'
    done
  done
}

show_targets() {
  printf 'targets :\n'
  aws elbv2 describe-target-health --target-group-arn "$1" --region "$region" \
    --query 'TargetHealthDescriptions[].[Target.Id,Target.Port,Target.AvailabilityZone,TargetHealth.State,TargetHealth.Reason]' \
    --output text \
  | awk 'BEGIN{printf "          %-16s %-6s %-18s %-12s %s\n","IP (ECS ghi)","PORT","AZ","STATE (ALB)","REASON"}
         {printf "          %-16s %-6s %-18s %-12s %s\n",$1,$2,$3,$4,($5==""||$5=="None"?"-":$5)}
         END{if(NR==0) printf "          (trong - chua task nao dang ky)\n"}'
}

for arn in $(resolve_arns "${1:-}"); do
  show_config "$arn"
  show_attributes "$arn"
  show_rules "$arn"
  show_targets "$arn"
done
echo
