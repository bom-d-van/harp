set -e

echo `date` start >> test.log

{{.SyncFiles}}
{{.SaveRelease}}
{{.RestartServer}}

echo `date` done >> test.log
sleep 1
