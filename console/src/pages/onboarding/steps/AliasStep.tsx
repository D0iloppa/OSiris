import { useEffect } from 'react'
import { Alert, Button, Form, Input, Space, Spin, Typography } from 'antd'
import { useAliasDefaults, useUpsertAliases } from '../../../hooks/useOnboarding'
import { StatusTag } from '../../../components/StatusTag'
import type { AliasEntryInput, OnboardingStep0Status } from '../../../api/types'

const { Text } = Typography

/**
 * 0-3 — `osiris/*` 별칭 확정 (`upsertAliases`, 초기값은
 * `getDefaultAliasMapping()`). 항목 수는 [계약 미확정] — I10이 "3개"라고만
 * 언급할 뿐 정확한 이름을 명시하지 않아, GET /onboarding/step0/alias-defaults
 * 응답을 그대로 편집 폼의 초기값으로 쓴다(하드코딩하지 않음).
 */
export function AliasStep({ step0 }: { step0: OnboardingStep0Status }) {
  const defaultsQuery = useAliasDefaults(true)
  const mutation = useUpsertAliases()
  const [form] = Form.useForm<{ entries: AliasEntryInput[] }>()

  useEffect(() => {
    if (defaultsQuery.data && !form.getFieldValue('entries')) {
      form.setFieldsValue({ entries: defaultsQuery.data })
    }
  }, [defaultsQuery.data, form])

  if (defaultsQuery.isLoading) return <Spin />
  if (defaultsQuery.isError) {
    return (
      <Alert
        type="error"
        showIcon
        message="별칭 기본값을 불러오지 못했습니다"
        description={(defaultsQuery.error as Error).message}
      />
    )
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      {step0.aliases.length > 0 && (
        <Space direction="vertical" size={4}>
          {step0.aliases.map((a) => (
            <div key={a.name} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <Text code style={{ fontFamily: 'var(--osiris-font-mono)' }}>
                {a.name}
              </Text>
              {a.inSync ? (
                <StatusTag kind="success">inSync</StatusTag>
              ) : (
                <StatusTag kind="error">providerMissing</StatusTag>
              )}
            </div>
          ))}
        </Space>
      )}

      <Form<{ entries: AliasEntryInput[] }>
        form={form}
        layout="vertical"
        onFinish={(values) => mutation.mutate(values.entries)}
      >
        <Form.List name="entries">
          {(fields) => (
            <Space direction="vertical" style={{ width: '100%' }}>
              {fields.map((field) => (
                <Space key={field.key} align="baseline" wrap>
                  <Form.Item
                    {...field}
                    name={[field.name, 'name']}
                    label={field.name === 0 ? '별칭' : undefined}
                  >
                    <Input disabled style={{ width: 160, fontFamily: 'var(--osiris-font-mono)' }} />
                  </Form.Item>
                  <Form.Item
                    {...field}
                    name={[field.name, 'provider']}
                    label={field.name === 0 ? '프로바이더' : undefined}
                  >
                    <Input style={{ width: 140 }} />
                  </Form.Item>
                  <Form.Item
                    {...field}
                    name={[field.name, 'model']}
                    label={field.name === 0 ? '모델' : undefined}
                  >
                    <Input style={{ width: 220 }} />
                  </Form.Item>
                </Space>
              ))}
            </Space>
          )}
        </Form.List>
        {mutation.isError && (
          <Alert
            type="error"
            showIcon
            message={(mutation.error as Error).message}
            style={{ margin: '12px 0' }}
          />
        )}
        <Button type="primary" htmlType="submit" loading={mutation.isPending}>
          별칭 적용
        </Button>
      </Form>
    </Space>
  )
}
