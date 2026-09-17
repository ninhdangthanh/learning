export AWS_REGION=ap-southeast-1
export AWS_DEFAULT_REGION=$AWS_REGION
export ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export PROJECT=fnb

save() { echo "export $1=$2" >> ~/aws-fnb/env.sh; export $1=$2; echo "$1 = $2"; }
