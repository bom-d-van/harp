set -e

echo `date` start >> test.log

{{.SyncFiles}}
{{.RestartServer}}

echo `date` done >> test.log
sleep 1
