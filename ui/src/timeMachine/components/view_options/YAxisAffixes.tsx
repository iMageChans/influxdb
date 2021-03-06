// Libraries
import React, {PureComponent, ChangeEvent} from 'react'

// Components
import {Input, FormElement} from '@influxdata/clockface'
import {Grid} from 'src/clockface'

// Types
import {Columns} from '@influxdata/clockface'

interface Props {
  prefix: string
  suffix: string
  onUpdateYAxisPrefix: (prefix: string) => void
  onUpdateYAxisSuffix: (suffix: string) => void
}

class YAxisAffixes extends PureComponent<Props> {
  public render() {
    const {prefix, suffix} = this.props

    return (
      <>
        <Grid.Column widthSM={Columns.Six}>
          <FormElement label="Y-Value's Prefix">
            <Input value={prefix} onChange={this.handleUpdateYAxisPrefix} />
          </FormElement>
        </Grid.Column>
        <Grid.Column widthSM={Columns.Six}>
          <FormElement label="Y-Value's Suffix">
            <Input value={suffix} onChange={this.handleUpdateYAxisSuffix} />
          </FormElement>
        </Grid.Column>
      </>
    )
  }

  private handleUpdateYAxisPrefix = (
    e: ChangeEvent<HTMLInputElement>
  ): void => {
    const {onUpdateYAxisPrefix} = this.props
    const prefix = e.target.value
    onUpdateYAxisPrefix(prefix)
  }

  private handleUpdateYAxisSuffix = (
    e: ChangeEvent<HTMLInputElement>
  ): void => {
    const {onUpdateYAxisSuffix} = this.props
    const suffix = e.target.value
    onUpdateYAxisSuffix(suffix)
  }
}

export default YAxisAffixes
