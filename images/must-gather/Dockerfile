FROM quay.io/openshift/origin-must-gather:latest

# Save original gather script
RUN mv /usr/bin/gather /usr/bin/gather_original

# Copy all collection scripts to /usr/bin
COPY utils/must-gather/* /usr/bin/

ENTRYPOINT /usr/bin/gather
