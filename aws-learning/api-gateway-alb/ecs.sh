#!/usr/bin/env bash
set -uo pipefail

region="${AWS_REGION:-ap-southeast-1}"
events_shown="${ECS_EVENTS:-4}"

tg_name_map=$(aws elbv2 describe-target-groups --region "$region" --output json 2>/dev/null \
  | jq -c '[.TargetGroups[] | {key: .TargetGroupArn, value: .TargetGroupName}] | from_entries')

show_cluster() {
  aws ecs describe-clusters --clusters "$1" --region "$region" --output json \
  | jq -r '.clusters[0] |
      "\n=== \(.clusterName) ===",
      "status  : \(.status)   services=\(.activeServicesCount)  running=\(.runningTasksCount)  pending=\(.pendingTasksCount)"'
}

show_service() {
  aws ecs describe-services --cluster "$1" --services "$2" --region "$region" --output json \
  | jq -r --argjson tg "$tg_name_map" --argjson n "$events_shown" '
      .services[0] |
      "\n--- \(.serviceName) ---",
      "state   : \(.status)   desired=\(.desiredCount) running=\(.runningCount) pending=\(.pendingCount)",
      "taskdef : \(.taskDefinition | split("/") | last)   launch=\(.launchType // .capacityProviderStrategy[0].capacityProvider)",
      (if (.loadBalancers | length) > 0 then
         .loadBalancers[0] |
         "lb      : \($tg[.targetGroupArn] // .targetGroupArn)   container=\(.containerName):\(.containerPort)"
       else "lb      : (khong gan target group)" end),
      "grace   : \(.healthCheckGracePeriodSeconds // "-")s   minHealthy=\(.deploymentConfiguration.minimumHealthyPercent)%  max=\(.deploymentConfiguration.maximumPercent)%",
      "deploy  : \([.deployments[] | "\(.status) \(.rolloutState // "-") d=\(.desiredCount)/r=\(.runningCount)"] | join("  |  "))",
      "events  :",
      (.events[0:$n][] | "          \(.createdAt[0:19])  \(.message | sub("^\\(service [^)]+\\) "; ""))")'
}

show_tasks() {
  local arns
  arns=$(aws ecs list-tasks --cluster "$1" --service-name "$2" --region "$region" \
         --query 'taskArns[]' --output text)
  if [ -z "$arns" ]; then
    printf 'tasks   : (khong co task nao)\n'
    return
  fi
  printf 'tasks   :\n'
  printf '          %-34s %-9s %-9s %-16s %-18s %s\n' "TASK ID" "STATUS" "HEALTH" "IP" "AZ" "STARTED"
  aws ecs describe-tasks --cluster "$1" --tasks $arns --region "$region" --output json \
  | jq -r '.tasks[] |
      "          \(.taskArn | split("/") | last | .[0:32] | . + "  " | .[0:34])" +
      "\(.lastStatus | . + "         " | .[0:10])" +
      "\(.healthStatus | . + "         " | .[0:10])" +
      "\((.attachments[0].details[]? | select(.name=="privateIPv4Address") | .value) // "-" | . + "                " | .[0:17])" +
      "\(.availabilityZone | . + "                  " | .[0:19])" +
      "\(.startedAt[0:19] // "-")"'
}

cluster="${1:-${CLUSTER:-fnb-cluster}}"
show_cluster "$cluster"
for svc in $(aws ecs list-services --cluster "$cluster" --region "$region" \
             --query 'serviceArns[]' --output text | tr '\t' '\n' | awk -F/ '{print $NF}'); do
  show_service "$cluster" "$svc"
  show_tasks "$cluster" "$svc"
done
echo
