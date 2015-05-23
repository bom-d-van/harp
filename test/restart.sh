set -e

echo $(date) start >> restart.log

{{.RestartServer}}

echo $(date) done >> restart.log
