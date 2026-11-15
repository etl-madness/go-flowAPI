%global debug_package %{nil}
%define __strip /bin/true

Name:           go-flow-api
Version:        %{_flow_version}
Release:        %{_flow_release}%{?dist}
Summary:        ETL Madness go-flowAPI REST, Artifact CRUD, Execution & Telemetry Service
License:        MIT
URL:            https://github.com/etl-madness/go-flowAPI
Source0:        go-flow-api-%{_flow_version}-staged.tar.gz

%description
ETL Madness go-flowAPI is the centralized REST API, database artifact repository,
live Server-Sent Events (SSE) execution gateway, and multi-database telemetry
service for the Flow Ecosystem. Installed isolated under /opt/flow-api alongside
systemd, OpenRC, and SysVinit service templates and /etc/go-flow-api/go-flow-api.env.

%prep
%setup -q -c -n go-flow-api-%{_flow_version}

%build
# Pre-compiled pure-Go static binary and compressed docs are staged in Source0.

%install
rm -rf %{buildroot}
install -d -m 0755 %{buildroot}/opt/flow-api/bin
install -m 0755 bin/go-flowAPI %{buildroot}/opt/flow-api/bin/go-flowAPI

install -d -m 0755 %{buildroot}/opt/flow-api/share/man/man1
install -m 0644 doc/man1/go-flowAPI.1.gz %{buildroot}/opt/flow-api/share/man/man1/go-flowAPI.1.gz

install -d -m 0755 %{buildroot}/opt/flow-api/share/man/man5
install -m 0644 doc/man5/go-flow-api.env.5.gz %{buildroot}/opt/flow-api/share/man/man5/go-flow-api.env.5.gz

install -d -m 0755 %{buildroot}/opt/flow-api/share/info
install -m 0644 doc/info/go-flow-api.info.gz %{buildroot}/opt/flow-api/share/info/go-flow-api.info.gz
install -m 0644 doc/info/dir %{buildroot}/opt/flow-api/share/info/dir

install -d -m 0755 %{buildroot}/opt/flow-api/share/sql
install -m 0644 sql/sqlite-init.sql %{buildroot}/opt/flow-api/share/sql/sqlite-init.sql
install -m 0644 sql/postgres-init.sql %{buildroot}/opt/flow-api/share/sql/postgres-init.sql
install -m 0644 sql/sqlserver-init.sql %{buildroot}/opt/flow-api/share/sql/sqlserver-init.sql

install -d -m 0755 %{buildroot}/opt/flow-api/share/doc
install -m 0644 guides/SCHEMA.md %{buildroot}/opt/flow-api/share/doc/SCHEMA.md
install -m 0644 guides/BOOTSTRAP.md %{buildroot}/opt/flow-api/share/doc/BOOTSTRAP.md
install -m 0644 guides/API_ENDPOINTS_REFERENCE.md %{buildroot}/opt/flow-api/share/doc/API_ENDPOINTS_REFERENCE.md
install -m 0644 guides/AD_KERBEROS_SIDECAR_GUIDE.md %{buildroot}/opt/flow-api/share/doc/AD_KERBEROS_SIDECAR_GUIDE.md

install -d -m 0755 %{buildroot}/opt/flow-api/share/services/systemd
install -m 0644 services/systemd/go-flow-api.service %{buildroot}/opt/flow-api/share/services/systemd/go-flow-api.service
install -d -m 0755 %{buildroot}/opt/flow-api/share/services/openrc
install -m 0755 services/openrc/go-flow-api %{buildroot}/opt/flow-api/share/services/openrc/go-flow-api
install -d -m 0755 %{buildroot}/opt/flow-api/share/services/sysvinit
install -m 0755 services/sysvinit/go-flow-api %{buildroot}/opt/flow-api/share/services/sysvinit/go-flow-api
install -d -m 0755 %{buildroot}/opt/flow-api/share/services/kerberos
install -m 0755 services/kerberos/go-flow-api-krb5-refresh.sh %{buildroot}/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh

install -d -m 0755 %{buildroot}/opt/flow-api/share/licenses
install -m 0644 LICENSE %{buildroot}/opt/flow-api/share/licenses/LICENSE

install -d -m 0755 %{buildroot}/etc/profile.d
install -m 0644 profile.d/go-flow-api.sh %{buildroot}/etc/profile.d/go-flow-api.sh

install -d -m 0750 %{buildroot}/etc/go-flow-api
install -m 0640 etc-env/go-flow-api.env %{buildroot}/etc/go-flow-api/go-flow-api.env

install -d -m 0755 %{buildroot}/usr/lib/systemd/system
install -m 0644 services/systemd/go-flow-api.service %{buildroot}/usr/lib/systemd/system/go-flow-api.service

install -d -m 0750 %{buildroot}/var/lib/go-flow-api
install -d -m 0750 %{buildroot}/var/log/go-flow-api

%pre
getent group flowapi >/dev/null 2>&1 || groupadd -r flowapi || :
getent passwd flowapi >/dev/null 2>&1 || useradd -r -g flowapi -d /var/lib/go-flow-api -s /sbin/nologin -c "ETL Madness go-flowAPI daemon" flowapi || :

%post
if getent passwd flowapi >/dev/null 2>&1; then
    chown flowapi:flowapi /var/lib/go-flow-api /var/log/go-flow-api 2>/dev/null || :
    chown root:flowapi /etc/go-flow-api /etc/go-flow-api/go-flow-api.env 2>/dev/null || :
fi
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || :
    if [ "$1" -ge 2 ] && systemctl is-active --quiet go-flow-api.service >/dev/null 2>&1; then
        systemctl try-restart go-flow-api.service >/dev/null 2>&1 || :
    fi
fi

%preun
if [ "$1" -eq 0 ]; then
    if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
        systemctl --no-reload disable --now go-flow-api.service >/dev/null 2>&1 || :
    fi
fi

%postun
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || :
fi

%files
%license /opt/flow-api/share/licenses/LICENSE
%dir /opt/flow-api
%dir /opt/flow-api/bin
/opt/flow-api/bin/go-flowAPI
%dir /opt/flow-api/share
%dir /opt/flow-api/share/man
%dir /opt/flow-api/share/man/man1
%dir /opt/flow-api/share/man/man5
/opt/flow-api/share/man/man1/go-flowAPI.1*
/opt/flow-api/share/man/man5/go-flow-api.env.5*
%dir /opt/flow-api/share/info
/opt/flow-api/share/info/go-flow-api.info*
/opt/flow-api/share/info/dir
%dir /opt/flow-api/share/sql
/opt/flow-api/share/sql/*.sql
%dir /opt/flow-api/share/doc
/opt/flow-api/share/doc/*.md
%dir /opt/flow-api/share/services
%dir /opt/flow-api/share/services/systemd
/opt/flow-api/share/services/systemd/go-flow-api.service
%dir /opt/flow-api/share/services/openrc
/opt/flow-api/share/services/openrc/go-flow-api
%dir /opt/flow-api/share/services/sysvinit
/opt/flow-api/share/services/sysvinit/go-flow-api
%dir /opt/flow-api/share/services/kerberos
/opt/flow-api/share/services/kerberos/go-flow-api-krb5-refresh.sh
%dir /opt/flow-api/share/licenses
/usr/lib/systemd/system/go-flow-api.service
%dir /etc/go-flow-api
%config(noreplace) /etc/go-flow-api/go-flow-api.env
%config(noreplace) /etc/profile.d/go-flow-api.sh
%dir /var/lib/go-flow-api
%dir /var/log/go-flow-api
