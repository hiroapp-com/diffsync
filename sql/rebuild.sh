#!/bin/sh
HIRODB=$1;
if [ -z $HIRODB ]; then 
    echo "usage: $0 <db path>";
    exit;
fi;
sqlite3 $HIRODB < dropall.sql
sqlite3 $HIRODB < users.sql
sqlite3 $HIRODB < contacts.sql
sqlite3 $HIRODB < notes.sql
sqlite3 $HIRODB < noterefs.sql
sqlite3 $HIRODB < sessions.sql
sqlite3 $HIRODB < tokens.sql
sqlite3 $HIRODB < stripe_tokens.sql
