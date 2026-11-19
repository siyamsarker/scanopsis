// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useCreateService, type Service, type ServiceCreate } from '../../api/admin';
import { ApiError } from '../../api/client';
import { describeError } from '../../api/errors';
import { Card, CardHeader } from '../../components/layout';
import { Field } from '../../components/Field';
import { monoInputClass, primaryButtonClass, secondaryButtonClass } from '../../ui';

type FieldName = 'path' | 'name' | 'sonar';

/** The API reports one message per 400; attach it to the field it talks about. */
function fieldOf(message: string): FieldName {
  const text = message.toLowerCase();
  if (text.includes('sonar')) return 'sonar';
  if (text.includes('name') && !text.includes('path')) return 'name';
  return 'path';
}

export function AddServiceForm({
  onCreated,
  onCancel,
}: {
  onCreated: (service: Service) => void;
  onCancel: () => void;
}) {
  const create = useCreateService();
  const [path, setPath] = useState('');
  const [name, setName] = useState('');
  const [sonar, setSonar] = useState('');
  const [errors, setErrors] = useState<Partial<Record<FieldName, string>>>({});
  const [failure, setFailure] = useState('');

  function submit(event: FormEvent) {
    event.preventDefault();
    setFailure('');
    if (!path.trim()) {
      setErrors({ path: 'Enter the GitLab project path, for example group/project.' });
      return;
    }
    setErrors({});
    const body: ServiceCreate = { gitlab_project_path: path.trim() };
    if (name.trim()) body.name = name.trim();
    if (sonar.trim()) body.sonar_project_key = sonar.trim();
    create.mutate(body, {
      onSuccess: onCreated,
      onError: (error) => {
        if (error instanceof ApiError && error.status === 400) {
          setErrors({ [fieldOf(error.message)]: error.message });
        } else {
          setFailure(describeError(error));
        }
      },
    });
  }

  return (
    <Card as="section" aria-label="Add a service" className="mb-6">
      <CardHeader
        title="Add a service"
        description="Add it now and the first upload from its pipeline fills in the rest."
      />
      <form onSubmit={submit} noValidate className="grid gap-4 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <Field
            id="service-path"
            label="GitLab project path"
            hint="group/project, as in the GitLab URL"
            error={errors.path}
            value={path}
            onChange={(e) => setPath(e.target.value)}
            className={monoInputClass}
            autoComplete="off"
            spellCheck={false}
            required
          />
        </div>
        <Field
          id="service-name"
          label="Name (optional)"
          hint="Defaults to the last part of the path."
          error={errors.name}
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoComplete="off"
        />
        <Field
          id="service-sonar"
          label="SonarQube project key (optional)"
          error={errors.sonar}
          value={sonar}
          onChange={(e) => setSonar(e.target.value)}
          className={monoInputClass}
          autoComplete="off"
          spellCheck={false}
        />
        {failure && (
          <p role="alert" className="flex items-start gap-2 text-text sm:col-span-2">
            <CircleAlert
              size={16}
              strokeWidth={1.5}
              aria-hidden="true"
              className="mt-1 shrink-0 text-fail"
            />
            {failure}
          </p>
        )}
        <div className="flex flex-wrap gap-2 sm:col-span-2">
          <button type="submit" className={primaryButtonClass} disabled={create.isPending}>
            {create.isPending ? 'Adding' : 'Add service'}
          </button>
          <button type="button" className={secondaryButtonClass} onClick={onCancel}>
            Cancel
          </button>
        </div>
      </form>
    </Card>
  );
}
