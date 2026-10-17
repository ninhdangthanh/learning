FNB_ENV_FILE="${FNB_ENV_FILE:-/Users/dangthanhninh/Documents/NinhData/aws-learn/aws-learning/api-gateway-alb/env.sh}"

save() { echo "export $1=$2" >> "$FNB_ENV_FILE"; export "$1=$2"; echo "$1 = $2"; }

export AWS_REGION=ap-southeast-1
export AWS_DEFAULT_REGION=$AWS_REGION
export PROJECT=fnb
export ACCOUNT_ID="${ACCOUNT_ID:-$(aws sts get-caller-identity --query Account --output text 2>/dev/null)}"

export VPC_ID=vpc-0378991f34c8bbe1d
export SUBNETS=subnet-0f47fd8e984168fa6,subnet-082044a51ee0c604f,subnet-0563bcbd2d995525d
export ECR_BASE=859928916043.dkr.ecr.ap-southeast-1.amazonaws.com
export ORDER_IMAGE=859928916043.dkr.ecr.ap-southeast-1.amazonaws.com/fnb/order-service:v1
export PRODUCT_IMAGE=859928916043.dkr.ecr.ap-southeast-1.amazonaws.com/fnb/product-service:v1
export EXEC_ROLE_ARN=arn:aws:iam::859928916043:role/ecsTaskExecutionRole-fnb
export ECS_SG=sg-0027cf7474fde0bc0
export MY_IP=171.253.177.38
export CLUSTER=fnb-cluster

get_task_ips() {
  aws ecs list-tasks --cluster $CLUSTER --service-name $1 --query 'taskArns[]' --output text \
  | xargs -n1 -I{} aws ecs describe-tasks --cluster $CLUSTER --tasks {} \
      --query 'tasks[0].attachments[0].details[?name==`networkInterfaceId`].value' --output text \
  | xargs -n1 -I{} aws ec2 describe-network-interfaces --network-interface-ids {} \
      --query 'NetworkInterfaces[0].Association.PublicIp' --output text
}
export ALB_SG=sg-0a4ef35188674781a
export ALB_ARN=arn:aws:elasticloadbalancing:ap-southeast-1:859928916043:loadbalancer/app/fnb-alb/50744bebb0dcbe41
export ALB_DNS=fnb-alb-1606210907.ap-southeast-1.elb.amazonaws.com
export ORDER_TG=arn:aws:elasticloadbalancing:ap-southeast-1:859928916043:targetgroup/fnb-order-tg/d4deb4b95bd90f01
export LISTENER_ARN=arn:aws:elasticloadbalancing:ap-southeast-1:859928916043:listener/app/fnb-alb/50744bebb0dcbe41/b04158646346ebc2
export PRODUCT_TG=arn:aws:elasticloadbalancing:ap-southeast-1:859928916043:targetgroup/fnb-product-tg/579a1f394a1a9716
export API_ID=lzwtbflg8i
export INT_ORDERS=utkab0t
export INT_ORDER_ID=te7r0v1
export INT_PRODUCTS=etdeljt
export INT_PRODUCT_ID=baelpt6
export R_GET_ORDERS=dqb33iu
export R_POST_ORDERS=t6mkky3
export R_GET_ORDER=zjdsjmu
export R_DEL_ORDER=5o5pf95
export R_GET_PRODUCTS=jeu0ev3
export R_POST_PRODUCTS=m2z876l
export R_GET_PRODUCT=k1mcpc8
export API_URL=https://lzwtbflg8i.execute-api.ap-southeast-1.amazonaws.com
export SYSINFO_LAMBDA_ARN=
export LAMBDA_ROLE_ARN=arn:aws:iam::859928916043:role/lambdaRole-fnb
export SYSINFO_LAMBDA_ARN=
export SYSINFO_LAMBDA_ARN=arn:aws:lambda:ap-southeast-1:859928916043:function:fnb-system-info
export INT_LAMBDA=x3uinfs
export R_SYSINFO=uw876wu
export POOL_ID=ap-southeast-1_ymKsiTC3j
export CLIENT_ID=6tijbu2vim29n0dlck6j8hnggs
export ISSUER=https://cognito-idp.ap-southeast-1.amazonaws.com/ap-southeast-1_ymKsiTC3j

get_token() {
  aws cognito-idp initiate-auth \
    --auth-flow USER_PASSWORD_AUTH \
    --client-id $CLIENT_ID \
    --auth-parameters USERNAME=nam,PASSWORD=Passw0rd123 \
    --query 'AuthenticationResult.IdToken' --output text
}
