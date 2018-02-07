JIRA Replicator
===============

A library (and tool) to automatically back-up your JIRA instance.

Especially handy for JIRA Cloud users.


Tool Usage
----------

### Configuration

Configuration is done using  the following environment variables.

* `JIRA_URL`
* `JIRA_USERNAME`
* `JIRA_PASSWORD`

### Usage

Generate the backup using:

    $ jira-backup backup
    
Download the backup using:

    $ jira-backup download -o FILE_PATH
    
Copy the backup to S3:

    $ jira-backup s3
    
Run it on a loop to make a backup every 48 hours:
    
    $ jira-backup daemon
   
Please see `jira-backup help s3` for full docs on all available options and how to set defaults.    

Backups on JIRA cloud are limited to once per 48 hours (after completion of previous backup).
If you exceed this you will be notified of when you can re-try the request.

### Library Usage

See godoc.